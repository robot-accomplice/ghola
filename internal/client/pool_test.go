package client

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// A Pool keeps the open connections bounded over many requests.
// DefaultStreamer and DefaultDoer build a transport for each request, and
// each one keeps an idle connection open until the process ends: a long
// run uses up the file descriptors.
func TestPool_KeepsTheOpenConnectionsBounded(t *testing.T) {
	var open atomic.Int64 // connections that the server has open now
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
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
	// net/http may open an extra connection while one goes back to the
	// idle pool, and it closes the extra ones. It keeps at most 2 idle
	// connections for each host.
	const limit = 2
	for deadline := time.Now().Add(2 * time.Second); open.Load() > limit && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open.Load(); n > limit {
		t.Errorf("%d requests keep %d connections open, want at most %d", 2*rounds, n, limit)
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
