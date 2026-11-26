package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// A Pool serves many requests over one connection. DefaultStreamer and
// DefaultDoer open a new connection for each request, and each idle
// connection stays open until the process ends: a long run uses up the
// file descriptors.
func TestPool_ReusesOneConnectionForStreamAndDo(t *testing.T) {
	var opened atomic.Int64
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	ts.Start()
	t.Cleanup(ts.Close)

	opts := &config.Options{URL: ts.URL}
	pool := &Pool{}
	const rounds = 20
	for range rounds {
		req := fasthttp.AcquireRequest()
		req.SetRequestURI(ts.URL)
		if _, err := pool.Stream(context.Background(), opts, req, io.Discard); err != nil {
			t.Fatal(err)
		}
		rsp := fasthttp.AcquireResponse()
		if err := pool.Do(context.Background(), opts, req, rsp); err != nil {
			t.Fatal(err)
		}
		Release(req, rsp)
	}
	// One connection, or two when a body is still in use as the next
	// request goes: never one for each request.
	if n := opened.Load(); n > 2 {
		t.Errorf("%d requests opened %d connections, want at most 2", 2*rounds, n)
	}
}

// A bad option reaches the caller from both entry points.
func TestPool_ReportsATransportError(t *testing.T) {
	opts := &config.Options{}
	opts.Stealth.HTTP3 = true // refused without --impersonate
	pool := &Pool{}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	if _, err := pool.Stream(context.Background(), opts, req, io.Discard); err == nil {
		t.Error("Stream: no error")
	}
	if err := pool.Do(context.Background(), opts, req, nil); err == nil {
		t.Error("Do: no error")
	}
}
