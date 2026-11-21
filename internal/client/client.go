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
// on the request object for a single attempt.
func (s *fetchState) prepareRequest(req *fasthttp.Request, opts *config.Options, effectiveURL, effectiveMethod, effectiveData string) {
	req.SetRequestURI(effectiveURL)
	req.SetBodyString(effectiveData)
	setHeaders(req, opts, s.activeProfile, s.headerOverrides, effectiveURL, effectiveMethod)
	req.Header.SetMethod(effectiveMethod)

	if _, overridden := s.headerOverrides["cookie"]; !overridden {
		if cookieHeader := s.jar.HeaderValue(effectiveURL); cookieHeader != "" {
			req.Header.Set("Cookie", cookieHeader)
		}
	}
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

	effectiveURL := opts.URL
	effectiveMethod := opts.Method
	effectiveData := opts.Data
	redirectsFollowed := 0
	start := time.Now() // --retry-max-time counts from the first attempt

redirectLoop:
	for {
		var lastErr error
		var nextWait time.Duration
		var haveNextWait bool

		for attempt := 0; attempt <= opts.Resilience.Retries; attempt++ {
			if err := ctx.Err(); err != nil {
				lastErr = err
				break
			}

			if attempt > 0 {
				wait := backoffWait(opts, attempt)
				if haveNextWait {
					wait = nextWait
					haveNextWait = false
				}
				if err := waitToRetry(ctx, opts, attempt, wait); err != nil {
					lastErr = err
					break
				}
			}

			req.Reset()
			rsp.Reset()

			if opts.Stealth.Drift > 0 {
				if err := applyDrift(ctx, opts.Stealth.Drift); err != nil {
					lastErr = err
					break
				}
			}

			state.prepareRequest(req, opts, effectiveURL, effectiveMethod, effectiveData)

			lastErr = state.do(ctx, &state.effectiveOpts, req, rsp)
			if lastErr != nil {
				if !retryTimeLeft(opts, start) {
					break
				}
				continue
			}
			state.jar.AbsorbResponse(effectiveURL, rsp)
			if out := opts.Output.HeaderOut; out != nil {
				if _, err := out.Write(rsp.Header.Header()); err != nil {
					lastErr = fmt.Errorf("--save-headers: %w", config.WriteError(err))
					break
				}
			}
			if err := state.jar.Save(); err != nil {
				lastErr = err
				break
			}

			status := rsp.StatusCode()
			if info := opts.Output.Info; info != nil {
				info.EffectiveURL, info.Status, info.Redirects = effectiveURL, status, redirectsFollowed
				info.ContentType = string(rsp.Header.ContentType())
			}

			if opts.Resilience.RetryHTTP && attempt < opts.Resilience.Retries && isRetryableHTTPStatus(opts, status) && retryTimeLeft(opts, start) {
				if d, ok := parseRetryAfter(rsp); ok {
					nextWait = d
					haveNextWait = true
				}
				continue
			}

			if opts.Resilience.Location && isRedirectStatus(status) {
				loc := string(rsp.Header.Peek("Location"))
				if err := checkRedirectScheme(effectiveURL, loc); err != nil {
					fasthttp.ReleaseRequest(req)
					fasthttp.ReleaseResponse(rsp)
					return nil, nil, err
				}
				if newURL, newMethod, newData, ok := handleRedirect(status, effectiveMethod, effectiveData, loc, effectiveURL); ok {
					// One more hop than --max-redirs is an error, as in curl
					// (audit finding H6). A 3xx without a usable Location is
					// the answer.
					if redirectsFollowed >= opts.Resilience.MaxRedirs {
						fasthttp.ReleaseRequest(req)
						fasthttp.ReleaseResponse(rsp)
						return nil, nil, tooManyRedirects(opts)
					}
					redirectsFollowed++
					effectiveURL = newURL
					effectiveMethod = newMethod
					effectiveData = newData
					continue redirectLoop
				}
			}

			return req, rsp, nil
		}

		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, lastErr
	}
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
// redirectSchemes. A Location that does not resolve is the caller's case.
func checkRedirectScheme(baseURL, location string) error {
	next, ok := resolveLocation(baseURL, location)
	if !ok {
		return nil
	}
	u, err := url.Parse(next)
	if err != nil || redirectSchemes[strings.ToLower(u.Scheme)] {
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
