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
	return ln, func(req *fasthttp.Request, resp *fasthttp.Response) error {
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
	failDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
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
		t.Errorf("server received %d requests, want 5", count)
	}
	if atomic.LoadInt32(&processed) != 1 {
		t.Errorf("processed %d responses, want exactly 1", processed)
	}
}

func TestRunConcurrent_AllFail(t *testing.T) {
	failDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
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
	failDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
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
	slowDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
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
	failDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
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

	slowDoer := func(req *fasthttp.Request, resp *fasthttp.Response) error {
		n := atomic.AddInt32(&started, 1)
		if n == 1 {
			resp.SetStatusCode(200)
			resp.SetBodyString("winner")
			close(gate)
			return nil
		}
		// Losers block until context cancels them or gate opens.
		// With or-done, they should be cancelled quickly.
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
