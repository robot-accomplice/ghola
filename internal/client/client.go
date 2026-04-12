// Package client provides the HTTP request engine for ghola, including
// retry with exponential backoff, temporal drift jitter, ghost identity
// signing, and concurrent request execution with clean shutdown.
package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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

	proxySelector, err := gholaproxy.NewSelector(opts.ProxyCfg.Proxy, opts.ProxyCfg.Auth, opts.ProxyCfg.File, opts.ProxyCfg.Strategy)
	if err != nil {
		return nil, err
	}
	selectedProxy, err := proxySelector.Select()
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

	return s, nil
}

// prepareRequest sets URI, body, headers, auth, cookies, and ghost signature
// on the request object for a single attempt.
func (s *fetchState) prepareRequest(req *fasthttp.Request, opts *config.Options, effectiveURL, effectiveMethod, effectiveData string) {
	req.SetRequestURI(effectiveURL)
	req.SetBodyString(effectiveData)

	for _, generated := range profile.BuildHeaders(s.activeProfile, profile.HeaderOptions{
		Method:         effectiveMethod,
		TargetURL:      effectiveURL,
		RefererMode:    opts.Stealth.Referer,
		ExplicitUA:     resolveUserAgent(opts, s.activeProfile),
		ExplicitLang:   opts.Stealth.AcceptLanguage,
		StealthHeaders: opts.Stealth.StealthHeaders,
	}) {
		if _, overridden := s.headerOverrides[strings.ToLower(generated.Key)]; overridden {
			continue
		}
		req.Header.Set(generated.Key, generated.Value)
	}

	for _, h := range opts.Headers {
		key, val, ok := parseHeader(h)
		if !ok {
			continue
		}
		req.Header.Set(key, val)
	}

	if _, overridden := s.headerOverrides["user-agent"]; !overridden {
		req.Header.Set("User-Agent", resolveUserAgent(opts, s.activeProfile))
	}

	if opts.Stealth.Ghost {
		applyGhostSign(req, effectiveURL)
	}

	if opts.User != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(opts.User))
		req.Header.Set("Authorization", "Basic "+auth)
	}

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

// FetchURL sends an HTTP request according to opts, with retry and jitter.
// The caller must release the returned Request and Response via
// fasthttp.ReleaseRequest / ReleaseResponse.
func FetchURL(ctx context.Context, opts *config.Options, do Doer) (*fasthttp.Request, *fasthttp.Response, error) {
	state, err := newFetchState(opts, do)
	if err != nil {
		return nil, nil, err
	}

	if opts.Resilience.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Resilience.Timeout)*time.Millisecond)
		defer cancel()
	}

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()

	effectiveURL := opts.URL
	effectiveMethod := opts.Method
	effectiveData := opts.Data
	redirectsFollowed := 0

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
				wait := time.Duration(opts.Resilience.Backoff<<uint(attempt-1)) * time.Millisecond
				if haveNextWait {
					wait = nextWait
					haveNextWait = false
				}
				if !opts.Output.Silent {
					fmt.Fprintf(os.Stderr, "Retry %d/%d in %v...\n", attempt, opts.Resilience.Retries, wait)
				}
				if err := sleepCtx(ctx, wait); err != nil {
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
				continue
			}
			state.jar.AbsorbResponse(effectiveURL, rsp)
			if err := state.jar.Save(); err != nil {
				lastErr = err
				break
			}

			status := rsp.StatusCode()

			if opts.Resilience.RetryHTTP && attempt < opts.Resilience.Retries && isRetryableHTTPStatus(status) {
				if d, ok := parseRetryAfter(rsp); ok {
					nextWait = d
					haveNextWait = true
				}
				continue
			}

			if opts.Resilience.Location && isRedirectStatus(status) && redirectsFollowed < opts.Resilience.MaxRedirs {
				loc := string(rsp.Header.Peek("Location"))
				if newURL, newMethod, newData, ok := handleRedirect(status, effectiveMethod, effectiveData, loc, effectiveURL); ok {
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
		key, val, ok := parseHeader(raw)
		if ok {
			overrides[strings.ToLower(key)] = val
		}
	}
	return overrides
}

func parseHeader(raw string) (string, string, bool) {
	i := strings.IndexByte(raw, ':')
	if i < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(raw[:i])
	val := strings.TrimSpace(raw[i+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
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

func isRetryableHTTPStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func parseRetryAfter(rsp *fasthttp.Response) (time.Duration, bool) {
	raw := string(rsp.Header.Peek("Retry-After"))
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

func applyGhostSign(req *fasthttp.Request, rawURL string) {
	id := fmt.Sprintf("%d-%s", time.Now().Unix(), rawURL)
	hash := sha256.Sum256([]byte(id))
	req.Header.Set("X-Ghola-Identity", hex.EncodeToString(hash[:]))
}

// RunConcurrent spawns opts.Concurrency goroutines, each calling FetchURL.
// The first successful response is passed to out; the remaining goroutines
// are cancelled immediately via the or-done pattern.
func orDone[T any](done <-chan struct{}, ch <-chan T) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		for {
			select {
			case <-done:
				return
			case v, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- v:
				case <-done:
					return
				}
			}
		}
	}()
	return out
}

func RunConcurrent(ctx context.Context, opts *config.Options, do Doer, out func(*fasthttp.Request, *fasthttp.Response)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		req *fasthttp.Request
		rsp *fasthttp.Response
		err error
	}

	results := make(chan result, opts.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, rsp, err := FetchURL(ctx, opts, do)
			if err != nil {
				return
			}
			select {
			case results <- result{req: req, rsp: rsp, err: nil}:
			case <-ctx.Done():
				fasthttp.ReleaseRequest(req)
				fasthttp.ReleaseResponse(rsp)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	winnerProcessed := false
	for r := range orDone(ctx.Done(), results) {
		if r.err != nil {
			continue
		}
		if !winnerProcessed {
			winnerProcessed = true
			out(r.req, r.rsp)
			cancel()
			continue
		}
		fasthttp.ReleaseRequest(r.req)
		fasthttp.ReleaseResponse(r.rsp)
	}

	if winnerProcessed {
		for r := range results {
			if r.err != nil {
				continue
			}
			fasthttp.ReleaseRequest(r.req)
			fasthttp.ReleaseResponse(r.rsp)
		}
	}
}
