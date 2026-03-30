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

// FetchURL sends an HTTP request according to opts, with retry and jitter.
// The context controls cancellation of retry backoff and drift sleeps; a
// cancelled context causes FetchURL to return ctx.Err() immediately.
// The caller must release the returned Request and Response via
// fasthttp.ReleaseRequest / ReleaseResponse.
func FetchURL(ctx context.Context, opts *config.Options, do Doer) (*fasthttp.Request, *fasthttp.Response, error) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Millisecond)
		defer cancel()
	}

	effectiveURL := opts.URL
	effectiveMethod := opts.Method
	effectiveData := opts.Data
	redirectsFollowed := 0

	headerOverrides := parseHeaderOverrides(opts.Headers)
	activeProfile := profile.BrowserProfile{}
	if opts.Impersonate != "" || opts.StealthHeaders {
		resolved, err := profile.Resolve(opts.Impersonate)
		if err != nil {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(rsp)
			return nil, nil, err
		}
		activeProfile = resolved
	}

	jar, err := gholacookies.New(opts.CookieJar)
	if err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}
	if err := jar.AddRequestCookies(opts.URL, opts.Cookies); err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}
	if err := jar.Save(); err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}

	proxySelector, err := gholaproxy.NewSelector(opts.Proxy, opts.ProxyAuth, opts.ProxyFile, opts.ProxyStrategy)
	if err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}
	selectedProxy, err := proxySelector.Select()
	if err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}
	effectiveOpts := *opts
	if selectedProxy != "" {
		effectiveOpts.Proxy = selectedProxy
	}
	transport, err := gholatransport.New(&effectiveOpts, selectedProxy)
	if err != nil {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, err
	}
	if do == nil {
		do = func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
			return transport.Do(ctx, req, resp)
		}
	}

redirectLoop:
	for {
		var lastErr error
		var nextWait time.Duration
		var haveNextWait bool

		for attempt := 0; attempt <= opts.Retries; attempt++ {
			if err := ctx.Err(); err != nil {
				lastErr = err
				break
			}

			if attempt > 0 {
				wait := time.Duration(opts.Backoff<<uint(attempt-1)) * time.Millisecond
				if haveNextWait {
					wait = nextWait
					haveNextWait = false
				}
				if !opts.Silent {
					fmt.Fprintf(os.Stderr, "Retry %d/%d in %v...\n", attempt, opts.Retries, wait)
				}
				if err := sleepCtx(ctx, wait); err != nil {
					lastErr = err
					break
				}
			}

			req.Reset()
			rsp.Reset()

			if opts.Drift > 0 {
				if err := applyDrift(ctx, opts.Drift); err != nil {
					lastErr = err
					break
				}
			}

			req.SetRequestURI(effectiveURL)
			req.SetBodyString(effectiveData)

			for _, generated := range profile.BuildHeaders(activeProfile, profile.HeaderOptions{
				Method:         effectiveMethod,
				TargetURL:      effectiveURL,
				RefererMode:    opts.Referer,
				ExplicitUA:     generatedUA(opts, activeProfile),
				ExplicitLang:   opts.AcceptLanguage,
				StealthHeaders: opts.StealthHeaders,
			}) {
				if _, overridden := headerOverrides[strings.ToLower(generated.Key)]; overridden {
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

			if _, overridden := headerOverrides["user-agent"]; !overridden {
				req.Header.Set("User-Agent", generatedUA(opts, activeProfile))
			}

			if opts.Ghost {
				applyGhostSign(req, effectiveURL)
			}

			if opts.User != "" {
				auth := base64.StdEncoding.EncodeToString([]byte(opts.User))
				req.Header.Set("Authorization", "Basic "+auth)
			}

			req.Header.SetMethod(effectiveMethod)
			if _, overridden := headerOverrides["cookie"]; !overridden {
				if cookieHeader := jar.HeaderValue(effectiveURL); cookieHeader != "" {
					req.Header.Set("Cookie", cookieHeader)
				}
			}

			lastErr = do(ctx, &effectiveOpts, req, rsp)
			if lastErr != nil {
				continue
			}
			jar.AbsorbResponse(effectiveURL, rsp)
			if err := jar.Save(); err != nil {
				lastErr = err
				break
			}

			status := rsp.StatusCode()

			// Retry on certain HTTP statuses (transport succeeded, but server asked us to wait).
			if opts.RetryHTTP && attempt < opts.Retries && isRetryableHTTPStatus(status) {
				if d, ok := parseRetryAfter(rsp); ok {
					nextWait = d
					haveNextWait = true
				}
				continue
			}

			// Follow redirects, returning only the final response.
			if opts.Location && isRedirectStatus(status) && redirectsFollowed < opts.MaxRedirs {
				if loc := string(rsp.Header.Peek("Location")); loc != "" {
					if newURL, ok := resolveLocation(effectiveURL, loc); ok {
						redirectsFollowed++
						switch status {
						case fasthttp.StatusSeeOther: // 303
							effectiveMethod = fasthttp.MethodGet
							effectiveData = ""
						case fasthttp.StatusMovedPermanently, fasthttp.StatusFound: // 301/302
							upperMethod := strings.ToUpper(effectiveMethod)
							if upperMethod != fasthttp.MethodGet && upperMethod != fasthttp.MethodHead {
								effectiveMethod = fasthttp.MethodGet
								effectiveData = ""
							}
						default:
							// 307/308 preserve method+body.
						}

						// For GET/HEAD, don't carry a stale request body.
						if strings.EqualFold(effectiveMethod, fasthttp.MethodGet) || strings.EqualFold(effectiveMethod, fasthttp.MethodHead) {
							effectiveData = ""
						}
						effectiveURL = newURL
						continue redirectLoop
					}
				}
			}

			return req, rsp, nil
		}

		// Retries exhausted (transport errors / context errors) for the current effective URL.
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		return nil, nil, lastErr
	}
}

func generatedUA(opts *config.Options, activeProfile profile.BrowserProfile) string {
	if opts.AgentExplicit {
		return opts.Agent
	}
	if strings.TrimSpace(activeProfile.UserAgent) != "" {
		return activeProfile.UserAgent
	}
	return opts.Agent
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
	case fasthttp.StatusMovedPermanently, // 301
		fasthttp.StatusFound,             // 302
		fasthttp.StatusSeeOther,          // 303
		fasthttp.StatusTemporaryRedirect, // 307
		fasthttp.StatusPermanentRedirect: // 308
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

	// Retry-After: <seconds>
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}

	// Retry-After: <http-date>
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}

	return 0, false
}

// sleepCtx blocks for d or until ctx is cancelled, whichever comes first.
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
		return nil
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
// are cancelled immediately via the or-done pattern (context cancellation)
// so they stop retrying and release resources.
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
				// Cancelled before we could deliver the response; ensure we don't leak pooled resources.
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

	// Process the first successful response via orDone, and release resources for any losers.
	for r := range orDone(ctx.Done(), results) {
		if r.err != nil {
			continue
		}
		if !winnerProcessed {
			winnerProcessed = true
			out(r.req, r.rsp) // caller-owned release (see cmd/ghola/main.go)
			cancel()
			continue
		}
		fasthttp.ReleaseRequest(r.req)
		fasthttp.ReleaseResponse(r.rsp)
	}

	// Drain any queued results after cancellation so any already-acquired pooled resources are released.
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
