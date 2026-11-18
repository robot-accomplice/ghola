package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

var stacks = []string{"", "chrome"}

func limitedTransport(t *testing.T, impersonate string, r config.ResilienceOptions) Transport {
	t.Helper()
	tr, err := New(&config.Options{
		Stealth:    config.StealthOptions{Impersonate: impersonate, Insecure: true},
		Resilience: r,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// get runs Do and reads the body, with a 10 s safety limit.
func get(t *testing.T, tr Transport, url string) error {
	t.Helper()
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	req.SetRequestURI(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tr.Do(ctx, req, rsp); err != nil {
		return err
	}
	_, err := ReadBody(rsp)
	return err
}

// stream runs Stream into io.Discard, with a 10 s safety limit.
func stream(t *testing.T, tr Transport, url string) error {
	t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := tr.Stream(ctx, req, io.Discard)
	return err
}

// --connect-timeout stops a connection that does not finish. The server here
// accepts TCP and never answers the TLS handshake, so only the connect limit
// can end the request.
func TestConnectTimeout_StopsAStalledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() //nolint:gocritic // held open until the listener closes
		}
	}()

	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			tr := limitedTransport(t, imp, config.ResilienceOptions{ConnectTimeout: 300 * time.Millisecond})
			start := time.Now()
			err := get(t, tr, "https://"+ln.Addr().String()+"/")
			if !errors.Is(err, ErrConnectTimeout) {
				t.Fatalf("err = %v, want %v", err, ErrConnectTimeout)
			}
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("took %v, want about 300ms", d)
			}
		})
	}
}

// --connect-timeout limits the connection phase only, as in curl. A server
// that connects at once and sends its headers after the limit must succeed.
func TestConnectTimeout_DoesNotLimitTheResponse(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		io.WriteString(w, "late") //nolint:errcheck
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			tr := limitedTransport(t, imp, config.ResilienceOptions{ConnectTimeout: 300 * time.Millisecond})
			if err := get(t, tr, srv.URL+"/"); err != nil {
				t.Fatalf("Do: %v", err)
			}
		})
	}
}

func fastSpeedTick(t *testing.T) {
	t.Helper()
	old := speedTick
	speedTick = 50 * time.Millisecond
	t.Cleanup(func() { speedTick = old })
}

// --speed-limit stops a body that trickles in, on stdout (Do) and on a
// download to a file (Stream). Without it, a server that sends one byte a
// second keeps ghola waiting for as long as --max-time allows.
func TestSpeedLimit_StopsASlowBody(t *testing.T) {
	fastSpeedTick(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 100; i++ {
			if _, err := io.WriteString(w, "x"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	r := config.ResilienceOptions{SpeedLimit: 1000, SpeedTime: time.Second}
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			tr := limitedTransport(t, imp, r)
			for name, run := range map[string]func(*testing.T, Transport, string) error{"Do": get, "Stream": stream} {
				start := time.Now()
				if err := run(t, tr, srv.URL+"/"); !errors.Is(err, ErrTooSlow) {
					t.Errorf("%s: err = %v, want %v", name, err, ErrTooSlow)
				}
				if d := time.Since(start); d > 5*time.Second {
					t.Errorf("%s: took %v, want about 1s", name, d)
				}
			}
		})
	}
}

// The wait for the headers counts toward --speed-time, as in curl: curl
// 8.18.0 stops a server that holds its headers for 3 s after --speed-time,
// with exit 28. A server that thinks too long is stopped on both stacks.
func TestSpeedLimit_CountsTheWaitForHeaders(t *testing.T) {
	fastSpeedTick(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
		}
		w.Write(make([]byte, 64<<10)) //nolint:errcheck
	}))
	defer srv.Close()

	r := config.ResilienceOptions{SpeedLimit: 1000, SpeedTime: time.Second}
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			start := time.Now()
			if err := get(t, limitedTransport(t, imp, r), srv.URL+"/"); !errors.Is(err, ErrTooSlow) {
				t.Fatalf("err = %v, want %v", err, ErrTooSlow)
			}
			if d := time.Since(start); d > 2500*time.Millisecond {
				t.Errorf("took %v, want about 1s", d)
			}
		})
	}
}

// A body that arrives faster than --speed-limit is not stopped.
func TestSpeedLimit_AllowsAFastBody(t *testing.T) {
	fastSpeedTick(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 15; i++ {
			if _, err := w.Write(make([]byte, 4<<10)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	}))
	defer srv.Close()

	r := config.ResilienceOptions{SpeedLimit: 1000, SpeedTime: time.Second}
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			if err := get(t, limitedTransport(t, imp, r), srv.URL+"/"); err != nil {
				t.Fatalf("Do: %v", err)
			}
		})
	}
}
