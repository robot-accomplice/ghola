// Package client provides the request engine for ghola: retries with
// exponential backoff, redirects, the cookie jar, and downloads to a file.
package client

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	gholacookies "github.com/robot-accomplice/ghola/internal/cookies"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholaproxy "github.com/robot-accomplice/ghola/internal/proxy"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Doer abstracts the HTTP round-trip so tests can inject a mock transport.
type Doer func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error

// DefaultDoer uses fasthttp.Do for real network calls.
func DefaultDoer(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
	tr, err := gholatransport.New(opts, "")
	if err != nil {
		return err
	}
	return tr.Do(ctx, req, resp)
}

// fetchState holds the pre-request initialization results shared across
// retry attempts and redirect hops.
type fetchState struct {
	jar             *gholacookies.Jar
	activeProfile   profile.BrowserProfile
	headerOverrides map[string]string
	effectiveOpts   config.Options
	do              Doer
}

// newFetchState performs all pre-request initialization: profile resolution,
// cookie jar setup, proxy selection, and transport construction.
func newFetchState(opts *config.Options, do Doer) (*fetchState, error) {
	s := &fetchState{
		headerOverrides: parseHeaderOverrides(opts.Headers),
		effectiveOpts:   *opts,
	}

	if opts.Stealth.Impersonate != "" || opts.Stealth.StealthHeaders {
		resolved, err := profile.Resolve(opts.Stealth.Impersonate)
		if err != nil {
			return nil, err
		}
		s.activeProfile = resolved
	}

	jar, err := gholacookies.New(opts.Stealth.CookieJar)
	if err != nil {
		return nil, err
	}
	if err := jar.AddRequestCookies(opts.URL, opts.Stealth.Cookies); err != nil {
		return nil, err
	}
	if err := jar.Save(); err != nil {
		return nil, err
	}
	s.jar = jar

	selectedProxy, err := selectProxy(opts)
	if err != nil {
		return nil, err
	}
	if selectedProxy != "" {
		s.effectiveOpts.ProxyCfg.Proxy = selectedProxy
	}

	transport, err := gholatransport.New(&s.effectiveOpts, selectedProxy)
	if err != nil {
		return nil, err
	}

	if do != nil {
		s.do = do
	} else {
		s.do = func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
			return transport.Do(ctx, req, resp)
		}
	}
	s.do = withAuthAnswer(s.do) // --auth digest and any

	return s, nil
}

// prepareRequest sets URI, body, headers, auth and cookies
// on the request object for a single attempt. previousURL is the URL of
// the hop before a redirect, "" on the first hop.
func (s *fetchState) prepareRequest(req *fasthttp.Request, opts *config.Options, effectiveURL, effectiveMethod, effectiveData, previousURL string) {
	req.SetRequestURI(effectiveURL)
	// An encoded slash (%2F) is part of a name, not a separator: the path
	// goes on the wire as the user wrote it.
	req.URI().DisablePathNormalizing = true
	req.SetBodyString(effectiveData)
	// The method comes first: the SigV4 signature in setHeaders covers it.
	req.Header.SetMethod(effectiveMethod)
	setHeaders(req, opts, s.activeProfile, s.headerOverrides, effectiveURL, effectiveMethod)
	if _, overridden := s.headerOverrides["referer"]; !overridden && opts.Stealth.Referer == "auto" && previousURL != "" {
		req.Header.Set("Referer", previousURL) // as curl -e ";auto"
	}
	if r := rangeHeader(opts.Output); r != "" {
		req.Header.Set("Range", r)
	}

	if _, overridden := s.headerOverrides["cookie"]; !overridden {
		if cookieHeader := s.jar.HeaderValue(effectiveURL); cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}
	}
}

// rangeHeader returns the Range header of --range or --continue-at on
// stdout, or "". --continue-at - needs a file to find the offset, so on
// stdout it starts at 0, as in curl.
func rangeHeader(out config.OutputOptions) string {
	switch {
	case out.Range != "":
		return "bytes=" + strings.TrimPrefix(out.Range, "bytes=")
	case out.ContinueAt != "" && out.ContinueAt != "-" && out.ContinueAt != "0":
		return "bytes=" + out.ContinueAt + "-"
	}
	return ""
}

// handleRedirect resolves the redirect target and determines the new HTTP
// method and body based on the status code.
func handleRedirect(status int, method, data, location, baseURL string) (newURL, newMethod, newData string, ok bool) {
	resolved, valid := resolveLocation(baseURL, location)
	if !valid {
		return "", "", "", false
	}

	newMethod = method
	newData = data

	switch status {
	case fasthttp.StatusSeeOther: // 303
		newMethod = fasthttp.MethodGet
		newData = ""
	case fasthttp.StatusMovedPermanently, fasthttp.StatusFound: // 301/302
		upper := strings.ToUpper(method)
		if upper != fasthttp.MethodGet && upper != fasthttp.MethodHead {
			newMethod = fasthttp.MethodGet
			newData = ""
		}
	default:
		// 307/308 preserve method+body.
	}

	if strings.EqualFold(newMethod, fasthttp.MethodGet) || strings.EqualFold(newMethod, fasthttp.MethodHead) {
		newData = ""
	}

	return resolved, newMethod, newData, true
}

// ErrTooManyRedirects is returned when -L reaches --max-redirs.
var ErrTooManyRedirects = errors.New("too many redirects")

func tooManyRedirects(opts *config.Options) error {
	return fmt.Errorf("%w: maximum (%d) redirects followed", ErrTooManyRedirects, opts.Resilience.MaxRedirs)
}

// WithTimeout applies --timeout to ctx. The deadline covers the whole
// transfer, the body included, as curl's --max-time does. The caller keeps
// ctx until it has read the body, because FetchURL returns the body as a
// stream that is read after FetchURL returns.
func WithTimeout(ctx context.Context, opts *config.Options) (context.Context, context.CancelFunc) {
	if opts.Resilience.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Duration(opts.Resilience.Timeout)*time.Millisecond)
}

// hop is the request of one redirect hop: its URL, method and body, and
// the URL before it ("" on the first hop).
type hop struct {
	url, method, data, previous string
	redirects                   int
}

// outcome is the result of one attempt.
type outcome int

const (
	answered    outcome = iota // the response is the result
	redirected                 // h holds the next hop
	retryError                 // no response: a retry may help
	retryStatus                // a retryable status: a retry may help, else the response stands
	failed                     // stop with the error
)

// FetchURL sends an HTTP request according to opts, with retry and jitter.
// It does not apply --timeout: use WithTimeout on ctx. The response body
// can be a stream that needs ctx to stay alive until it is read.
// The caller must release the returned Request and Response via
// fasthttp.ReleaseRequest / ReleaseResponse.
func FetchURL(ctx context.Context, opts *config.Options, do Doer) (*fasthttp.Request, *fasthttp.Response, error) {
	// A negative count would run no attempt and return no response and no
	// error (audit finding H5). The bridge reaches this point without the
	// CLI's flag check.
	if opts.Resilience.Retries < 0 {
		return nil, nil, fmt.Errorf("retries cannot be negative: %d", opts.Resilience.Retries)
	}
	state, err := newFetchState(opts, do)
	if err != nil {
		return nil, nil, err
	}

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	h := hop{url: opts.URL, method: opts.Method, data: opts.Data}
	start := time.Now() // --retry-max-time counts from the first attempt

	for attempt := 0; ; {
		out, wait, err := state.try(ctx, opts, req, rsp, &h, attempt)
		if out == answered || (out == retryStatus && !mayRetry(ctx, opts, attempt, start, wait)) {
			return req, rsp, nil
		}
		switch {
		case out == redirected:
			attempt = 0
			continue
		case out == failed, !mayRetry(ctx, opts, attempt, start, wait):
		default:
			attempt++
			if err = waitToRetry(ctx, opts, attempt, wait); err == nil {
				continue
			}
		}
		Release(req, rsp)
		return nil, nil, err
	}
}

// mayRetry reports whether retry number attempt+1 may start after wait:
// a retry is left, the wait ends inside --retry-max-time, and the wait
// ends before the deadline of ctx. A wait that cannot be followed by a
// request does not run, as in curl.
func mayRetry(ctx context.Context, opts *config.Options, attempt int, start time.Time, wait time.Duration) bool {
	if attempt >= opts.Resilience.Retries {
		return false
	}
	if limit := opts.Resilience.RetryMaxTime; limit > 0 && time.Since(start)+wait >= limit {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Now().Add(wait).After(deadline) {
		return false
	}
	return true
}

// try sends one attempt of hop h. For a retry it returns the wait before
// it: --retry-delay doubled for each retry, or the Retry-After of the
// server.
func (s *fetchState) try(ctx context.Context, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response, h *hop, attempt int) (outcome, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return failed, 0, err
	}
	req.Reset()
	rsp.Reset()
	if opts.Stealth.Drift > 0 {
		if err := applyDrift(ctx, opts.Stealth.Drift); err != nil {
			return failed, 0, err
		}
	}
	s.prepareRequest(req, opts, h.url, h.method, h.data, h.previous)
	wait := backoffWait(opts, attempt+1)
	if err := s.do(ctx, &s.effectiveOpts, req, rsp); err != nil {
		return retryError, wait, err
	}
	if err := s.absorb(opts, rsp, h); err != nil {
		return failed, 0, err
	}
	status := rsp.StatusCode()
	if opts.Resilience.RetryHTTP && isRetryableHTTPStatus(opts, status) {
		if d, ok := parseRetryAfter(rsp); ok {
			wait = d
		}
		return retryStatus, wait, nil
	}
	if opts.Resilience.Location && isRedirectStatus(status) {
		return nextHop(opts, rsp, h)
	}
	return answered, 0, nil
}

// absorb takes the cookies of the response, writes its head to
// --save-headers, and records it for --output-template.
func (s *fetchState) absorb(opts *config.Options, rsp *fasthttp.Response, h *hop) error {
	s.jar.AbsorbResponse(h.url, rsp)
	if out := opts.Output.HeaderOut; out != nil {
		if _, err := out.Write(rsp.Header.Header()); err != nil {
			return fmt.Errorf("--save-headers: %w", config.WriteError(err))
		}
	}
	if err := s.jar.Save(); err != nil {
		return err
	}
	if info := opts.Output.Info; info != nil {
		info.EffectiveURL, info.Status, info.Redirects = h.url, rsp.StatusCode(), h.redirects
		info.ContentType = string(rsp.Header.ContentType())
	}
	return nil
}

// nextHop moves h to the target of a redirect answer. A 3xx without a
// usable Location is the answer.
func nextHop(opts *config.Options, rsp *fasthttp.Response, h *hop) (outcome, time.Duration, error) {
	loc := string(rsp.Header.Peek("Location"))
	if err := checkRedirectScheme(opts, h.url, loc); err != nil {
		return failed, 0, err
	}
	newURL, newMethod, newData, ok := handleRedirect(rsp.StatusCode(), h.method, h.data, loc, h.url)
	if !ok {
		return answered, 0, nil
	}
	// One more hop than --max-redirs is an error, as in curl (audit
	// finding H6).
	if h.redirects >= opts.Resilience.MaxRedirs {
		return failed, 0, tooManyRedirects(opts)
	}
	*h = hop{url: newURL, method: newMethod, data: newData, previous: h.url, redirects: h.redirects + 1}
	return redirected, 0, nil
}

// Release gives the request and the answer of FetchURL back.
func Release(req *fasthttp.Request, rsp *fasthttp.Response) {
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(rsp)
}

// selectProxy returns the proxy from --proxy or --proxy-file, or "" when
// neither is set. Then the transport takes the proxy from the environment.
// FetchURL and the download path both call it, so a proxy cannot reach one
// path and miss the other (audit finding M8).
func selectProxy(opts *config.Options) (string, error) {
	selector, err := gholaproxy.NewSelector(opts.ProxyCfg.Proxy, opts.ProxyCfg.Auth, opts.ProxyCfg.File, opts.ProxyCfg.Strategy)
	if err != nil {
		return "", err
	}
	if opts.ProxyCfg.HealthCheck {
		if err := selector.HealthCheck(); err != nil {
			return "", err
		}
	}
	return selector.Select()
}

func resolveUserAgent(opts *config.Options, activeProfile profile.BrowserProfile) string {
	if opts.Stealth.AgentExplicit {
		return opts.Stealth.Agent
	}
	if strings.TrimSpace(activeProfile.UserAgent) != "" {
		return activeProfile.UserAgent
	}
	return opts.Stealth.Agent
}

func parseHeaderOverrides(headers []string) map[string]string {
	overrides := make(map[string]string, len(headers))
	for _, raw := range headers {
		key, val, ok := config.ParseHeader(raw)
		if ok {
			overrides[strings.ToLower(key)] = val
		}
	}
	return overrides
}

func isRedirectStatus(status int) bool {
	switch status {
	case fasthttp.StatusMovedPermanently,
		fasthttp.StatusFound,
		fasthttp.StatusSeeOther,
		fasthttp.StatusTemporaryRedirect,
		fasthttp.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// ErrRedirectScheme marks a redirect to a protocol that ghola does not
// follow. A server must not send ghola to file:// (a local file) or to any
// other protocol: curl follows only http, https, ftp and ftps, and stops
// with "Protocol not supported" (exit 1).
var ErrRedirectScheme = errors.New("redirect to a protocol that ghola does not follow")

// redirectSchemes are the protocols that a redirect may go to: curl's
// default list.
var redirectSchemes = map[string]bool{"http": true, "https": true, "ftp": true, "ftps": true}

// checkRedirectScheme refuses a redirect to a protocol outside
// redirectSchemes, or outside http and https with HTTPRedirectsOnly (the
// bridge). A Location that does not resolve is the caller's case.
func checkRedirectScheme(opts *config.Options, baseURL, location string) error {
	next, ok := resolveLocation(baseURL, location)
	if !ok {
		return nil
	}
	u, err := url.Parse(next)
	if err != nil {
		return nil
	}
	scheme := strings.ToLower(u.Scheme)
	if redirectSchemes[scheme] && (!opts.Resilience.HTTPRedirectsOnly || scheme == "http" || scheme == "https") {
		return nil
	}
	return fmt.Errorf("%w: %s (Location %q)", ErrRedirectScheme, u.Scheme, location)
}

func resolveLocation(baseURL, location string) (string, bool) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(ref)
	if resolved == nil {
		return "", false
	}
	return resolved.String(), true
}

// backoffWait returns the wait before retry number attempt (1 for the first
// retry): --backoff milliseconds, doubled for each retry after the first.
func backoffWait(opts *config.Options, attempt int) time.Duration {
	return time.Duration(opts.Resilience.Backoff<<uint(attempt-1)) * time.Millisecond
}

// retryTimeLeft reports whether --retry-max-time still allows a retry. As
// in curl, no retry starts after the limit, counted from the first attempt.
func retryTimeLeft(opts *config.Options, start time.Time) bool {
	limit := opts.Resilience.RetryMaxTime
	return limit <= 0 || time.Since(start) < limit
}

// waitToRetry announces retry number attempt on stderr and waits.
func waitToRetry(ctx context.Context, opts *config.Options, attempt int, wait time.Duration) error {
	opts.Output.Events.Retry(opts.URL, attempt, opts.Resilience.Retries, wait)
	if !opts.Output.Silent {
		fmt.Fprintf(os.Stderr, "Retry %d/%d in %v...\n", attempt, opts.Resilience.Retries, wait)
	}
	return sleepCtx(ctx, wait)
}

// isRetryableHTTPStatus reports a status that a retry may cure: the list of
// --retry-status, else the v0 list.
func isRetryableHTTPStatus(opts *config.Options, status int) bool {
	if list := opts.Resilience.RetryStatus; len(list) > 0 {
		return slices.Contains(list, status)
	}
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func parseRetryAfter(rsp *fasthttp.Response) (time.Duration, bool) {
	return retryAfter(string(rsp.Header.Peek("Retry-After")))
}

// retryAfter parses a Retry-After value: seconds, or an HTTP date.
func retryAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func applyDrift(ctx context.Context, driftMs int) error {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(driftMs)))
	if err != nil {
		return fmt.Errorf("drift jitter: %w", err)
	}
	return sleepCtx(ctx, time.Duration(n.Int64())*time.Millisecond)
}
