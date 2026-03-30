package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func bg() context.Context { return context.Background() }

func newTestServer(handler fasthttp.RequestHandler) (*fasthttputil.InmemoryListener, Doer) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: handler}
	go srv.Serve(ln) //nolint:errcheck

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) {
			return ln.Dial()
		},
	}
	return ln, func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return c.Do(req, resp)
	}
}

func TestFetchURL_BasicGET(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("hello")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "GET",
		Agent:  "ghola-test",
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if rsp.StatusCode() != 200 {
		t.Errorf("status = %d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "hello" {
		t.Errorf("body = %q, want hello", rsp.Body())
	}
}

func TestFetchURL_CustomHeaders(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Request.Header.Peek("X-Custom")) != "value" {
			t.Error("X-Custom header missing or wrong")
		}
		if string(ctx.Request.Header.Peek("User-Agent")) != "test-agent" {
			t.Errorf("User-Agent = %q, want test-agent", ctx.Request.Header.Peek("User-Agent"))
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "test-agent",
		Headers: []string{"X-Custom: value"},
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_PostWithBody(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Method()) != "POST" {
			t.Errorf("method = %q, want POST", ctx.Method())
		}
		if string(ctx.PostBody()) != "test-data" {
			t.Errorf("body = %q, want test-data", ctx.PostBody())
		}
		ctx.SetStatusCode(201)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "POST",
		Data:   "test-data",
		Agent:  "ghola",
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if rsp.StatusCode() != 201 {
		t.Errorf("status = %d, want 201", rsp.StatusCode())
	}
}

func TestFetchURL_GhostSign(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		ghost := string(ctx.Request.Header.Peek("X-Ghola-Identity"))
		if ghost == "" {
			t.Error("X-Ghola-Identity header missing")
		}
		if len(ghost) != 64 {
			t.Errorf("ghost hash length = %d, want 64 hex chars", len(ghost))
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "GET",
		Agent:  "ghola",
		Ghost:  true,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_NoGhostByDefault(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if len(ctx.Request.Header.Peek("X-Ghola-Identity")) > 0 {
			t.Error("X-Ghola-Identity should not be set when ghost=false")
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "GET",
		Agent:  "ghola",
		Ghost:  false,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_BasicAuth(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		auth := string(ctx.Request.Header.Peek("Authorization"))
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:secret"))
		if auth != want {
			t.Errorf("Authorization = %q, want %q", auth, want)
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "GET",
		Agent:  "ghola",
		User:   "admin:secret",
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_Retries(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			ctx.SetStatusCode(500)
			ctx.Conn().Close()
			return
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("success")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Retries: 3,
		Backoff: 1,
		Silent:  true,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL should succeed after retries: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if string(rsp.Body()) != "success" {
		t.Errorf("body = %q, want success", rsp.Body())
	}
}

func TestFetchURL_RetriesExhausted(t *testing.T) {
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return fmt.Errorf("connection refused")
	}

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Retries: 1,
		Backoff: 1,
		Silent:  true,
	}

	req, rsp, err := FetchURL(bg(), opts, failDoer)
	if err == nil {
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)
		t.Fatal("expected error when retries exhausted")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %q, want connection refused", err)
	}
}

func TestFetchURL_RetryHTTP_RetryAfterPrecedence(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			ctx.Response.Header.Set("Retry-After", "0")
			ctx.SetStatusCode(429)
			return
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:       "http://test",
		Method:    "GET",
		Agent:     "ghola",
		Retries:   1,
		Backoff:   10000, // would be large if exponential backoff were used
		RetryHTTP: true,
		Silent:    true,
	}

	start := time.Now()
	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if rsp.StatusCode() != 200 {
		t.Fatalf("status=%d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "ok" {
		t.Fatalf("body=%q, want ok", rsp.Body())
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected Retry-After to avoid long backoff; elapsed=%v", elapsed)
	}
}

func TestFetchURL_RetryHTTP_RetryableStatusExhaustedReturnsResponse(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		atomic.AddInt32(&attempts, 1)
		ctx.SetStatusCode(503)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:       "http://test",
		Method:    "GET",
		Agent:     "ghola",
		Retries:   1,
		Backoff:   1,
		RetryHTTP: true,
		Silent:    true,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v (expected nil error since --fail is not set)", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if rsp.StatusCode() != 503 {
		t.Fatalf("status=%d, want 503", rsp.StatusCode())
	}
}

func TestFetchURL_RedirectLocationRelative(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		n := atomic.AddInt32(&attempts, 1)
		switch n {
		case 1:
			ctx.SetStatusCode(302)
			ctx.Response.Header.Set("Location", "/next")
		default:
			if string(ctx.Path()) != "/next" {
				t.Fatalf("path=%q, want /next", ctx.Path())
			}
			ctx.SetStatusCode(200)
			ctx.SetBodyString("ok")
		}
	})
	defer ln.Close()

	opts := &config.Options{
		URL:         "http://test/start",
		Method:      "GET",
		Agent:       "ghola",
		Retries:     0,
		Location:    true,
		MaxRedirs:   5,
		Silent:      true,
		RetryHTTP:   false,
		Backoff:     1,
		Ghost:       false,
		Fail:        false,
		Include:     false,
		Snoop:       false,
		Concurrency: 1,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if rsp.StatusCode() != 200 {
		t.Fatalf("status=%d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "ok" {
		t.Fatalf("body=%q, want ok", rsp.Body())
	}
}

func TestFetchURL_RedirectMaxRedirsExceededReturnsLastResponse(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		atomic.AddInt32(&attempts, 1)
		ctx.SetStatusCode(302)
		ctx.Response.Header.Set("Location", "/loop")
		ctx.SetBodyString("redir")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:         "http://test/start",
		Method:      "GET",
		Agent:       "ghola",
		Retries:     0,
		Location:    true,
		MaxRedirs:   1,
		Silent:      true,
		RetryHTTP:   false,
		Backoff:     1,
		Concurrency: 1,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if rsp.StatusCode() != 302 {
		t.Fatalf("status=%d, want 302", rsp.StatusCode())
	}
	if string(rsp.Body()) != "redir" {
		t.Fatalf("body=%q, want redir", rsp.Body())
	}
}

func TestFetchURL_Redirect303RewritesToGET(t *testing.T) {
	var attempts int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		n := atomic.AddInt32(&attempts, 1)
		switch n {
		case 1:
			ctx.SetStatusCode(303)
			ctx.Response.Header.Set("Location", "/next")
		default:
			if string(ctx.Method()) != "GET" {
				t.Fatalf("method=%q, want GET", ctx.Method())
			}
			if len(ctx.PostBody()) != 0 {
				t.Fatalf("post body length=%d, want 0", len(ctx.PostBody()))
			}
			ctx.SetStatusCode(200)
			ctx.SetBodyString("ok")
		}
	})
	defer ln.Close()

	opts := &config.Options{
		URL:         "http://test/start",
		Method:      "POST",
		Data:        "abc",
		Agent:       "ghola",
		Retries:     0,
		Location:    true,
		MaxRedirs:   5,
		Silent:      true,
		RetryHTTP:   false,
		Backoff:     1,
		Concurrency: 1,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts=%d, want 2", attempts)
	}
	if rsp.StatusCode() != 200 {
		t.Fatalf("status=%d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "ok" {
		t.Fatalf("body=%q, want ok", rsp.Body())
	}
}

func TestFetchURL_Drift(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:    "http://test",
		Method: "GET",
		Agent:  "ghola",
		Drift:  1,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL with drift error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if rsp.StatusCode() != 200 {
		t.Errorf("status = %d, want 200", rsp.StatusCode())
	}
}

func TestFetchURL_MultipleHeaders(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Request.Header.Peek("X-A")) != "1" {
			t.Error("X-A header missing")
		}
		if string(ctx.Request.Header.Peek("X-B")) != "2" {
			t.Error("X-B header missing")
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Headers: []string{"X-A: 1", "X-B: 2"},
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_MalformedHeaderIgnored(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Headers: []string{"no-colon-separator"},
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_CustomHeadersNoSpaceAfterColon(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Request.Header.Peek("X-Custom")) != "value" {
			t.Errorf("X-Custom header = %q, want value", ctx.Request.Header.Peek("X-Custom"))
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Headers: []string{"X-Custom:value"},
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestRunConcurrent_AllGoroutinesComplete(t *testing.T) {
	var count int32
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		atomic.AddInt32(&count, 1)
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:         "http://test",
		Method:      "GET",
		Agent:       "ghola",
		Concurrency: 5,
	}

	var processed int32
	RunConcurrent(bg(), opts, do, func(req *fasthttp.Request, rsp *fasthttp.Response) {
		atomic.AddInt32(&processed, 1)
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)
	})

	if atomic.LoadInt32(&count) != 5 {
		// Cancellation happens as soon as the first request succeeds, so not all
		// goroutines necessarily reach the network call before ctx is canceled.
		if atomic.LoadInt32(&count) < 1 {
			t.Errorf("server received %d requests, want at least 1", count)
		}
		if atomic.LoadInt32(&count) > 5 {
			t.Errorf("server received %d requests, want at most 5", count)
		}
	}
	if atomic.LoadInt32(&processed) != 1 {
		t.Errorf("processed %d responses, want exactly 1", processed)
	}
}

func TestRunConcurrent_AllFail(t *testing.T) {
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return fmt.Errorf("fail")
	}

	opts := &config.Options{
		URL:         "http://test",
		Method:      "GET",
		Agent:       "ghola",
		Concurrency: 3,
		Silent:      true,
	}

	var processed int32
	RunConcurrent(bg(), opts, failDoer, func(req *fasthttp.Request, rsp *fasthttp.Response) {
		atomic.AddInt32(&processed, 1)
	})

	if atomic.LoadInt32(&processed) != 0 {
		t.Errorf("processed %d responses, want 0 when all fail", processed)
	}
}

func TestFetchURL_ZeroRetries(t *testing.T) {
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return fmt.Errorf("fail")
	}

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Retries: 0,
		Silent:  true,
	}

	_, _, err := FetchURL(bg(), opts, failDoer)
	if err == nil {
		t.Fatal("expected error with zero retries")
	}
}

func TestFetchURL_CancelledContextStopsRetries(t *testing.T) {
	var attempts int32
	slowDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		atomic.AddInt32(&attempts, 1)
		return fmt.Errorf("fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Retries: 100,
		Backoff: 60000,
		Silent:  true,
	}

	start := time.Now()
	_, _, err := FetchURL(ctx, opts, slowDoer)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("FetchURL took %v; cancelled context should abort immediately", elapsed)
	}
	if atomic.LoadInt32(&attempts) > 1 {
		t.Errorf("attempts = %d; cancelled context should prevent retries", attempts)
	}
}

func TestFetchURL_CancelDuringBackoff(t *testing.T) {
	var attempts int32
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		atomic.AddInt32(&attempts, 1)
		return fmt.Errorf("fail")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	opts := &config.Options{
		URL:     "http://test",
		Method:  "GET",
		Agent:   "ghola",
		Retries: 10,
		Backoff: 10000,
		Silent:  true,
	}

	start := time.Now()
	_, _, err := FetchURL(ctx, opts, failDoer)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("FetchURL took %v; should abort during backoff sleep", elapsed)
	}
}

func TestRunConcurrent_CancelsLosers(t *testing.T) {
	var started int32
	gate := make(chan struct{})

	slowDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		n := atomic.AddInt32(&started, 1)
		if n == 1 {
			resp.SetStatusCode(200)
			resp.SetBodyString("winner")
			close(gate)
			return nil
		}
		<-gate
		return fmt.Errorf("context cancelled")
	}

	opts := &config.Options{
		URL:         "http://test",
		Method:      "GET",
		Agent:       "ghola",
		Concurrency: 5,
		Silent:      true,
	}

	var processed int32
	start := time.Now()
	RunConcurrent(bg(), opts, slowDoer, func(req *fasthttp.Request, rsp *fasthttp.Response) {
		atomic.AddInt32(&processed, 1)
	})
	elapsed := time.Since(start)

	if atomic.LoadInt32(&processed) != 1 {
		t.Errorf("processed = %d, want exactly 1", processed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("RunConcurrent took %v; losers should be cancelled quickly", elapsed)
	}
}

func TestSleepCtx_NormalCompletion(t *testing.T) {
	err := sleepCtx(bg(), 1*time.Millisecond)
	if err != nil {
		t.Errorf("sleepCtx returned error for normal sleep: %v", err)
	}
}

func TestSleepCtx_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := sleepCtx(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("sleepCtx took %v; should return immediately", elapsed)
	}
}

func TestFetchURL_ImpersonationAddsBrowserHeaders(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if !strings.Contains(string(ctx.Request.Header.Peek("User-Agent")), "Chrome/146") {
			t.Errorf("user-agent=%q, want browser-like chrome UA", ctx.Request.Header.Peek("User-Agent"))
		}
		if string(ctx.Request.Header.Peek("Sec-Fetch-Mode")) != "navigate" {
			t.Errorf("sec-fetch-mode=%q, want navigate", ctx.Request.Header.Peek("Sec-Fetch-Mode"))
		}
		if string(ctx.Request.Header.Peek("Accept-Language")) != "en-US,en;q=0.9" {
			t.Errorf("accept-language=%q, want en-US,en;q=0.9", ctx.Request.Header.Peek("Accept-Language"))
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:            "http://test",
		Method:         "GET",
		Agent:          "ghola",
		Impersonate:    "chrome",
		StealthHeaders: true,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
}

func TestFetchURL_DefaultPathStillUsesInjectedLegacyTransport(t *testing.T) {
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Request.Header.Peek("User-Agent")) != "ghola-test" {
			t.Errorf("user-agent=%q, want ghola-test", ctx.Request.Header.Peek("User-Agent"))
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("ok")
	})
	defer ln.Close()

	opts := &config.Options{
		URL:        "http://test",
		Method:     "GET",
		Agent:      "ghola-test",
		BufferSize: 8192,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if rsp.StatusCode() != 200 {
		t.Fatalf("status=%d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "ok" {
		t.Fatalf("body=%q, want ok", rsp.Body())
	}
}

func TestFetchURL_CookieJarPersistsAcrossRequests(t *testing.T) {
	jarPath := t.TempDir() + "/cookies.json"
	var seenCookie string
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		seenCookie = string(ctx.Request.Header.Peek("Cookie"))
		if seenCookie == "" {
			ctx.Response.Header.Add("Set-Cookie", "session=abc; Path=/")
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts := &config.Options{
		URL:       "http://test",
		Method:    "GET",
		Agent:     "ghola",
		CookieJar: jarPath,
	}

	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("first FetchURL error: %v", err)
	}
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(rsp)

	req, rsp, err = FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("second FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if !strings.Contains(seenCookie, "session=abc") {
		t.Fatalf("cookie header=%q, want persisted session cookie", seenCookie)
	}
}
