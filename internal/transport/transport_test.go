package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestNewSelectsSimpleTransportWithoutImpersonation(t *testing.T) {
	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tr.Name() != "fasthttp" {
		t.Fatalf("transport name=%q, want fasthttp", tr.Name())
	}
}

func TestNewSelectsTLSClientTransportWithImpersonation(t *testing.T) {
	tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: "chrome"}}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tr.Name() != "tls-client" {
		t.Fatalf("transport name=%q, want tls-client", tr.Name())
	}
}

func TestNewPreservesBufferSizeOnSimpleTransport(t *testing.T) {
	tr, err := New(&config.Options{Resilience: config.ResilienceOptions{BufferSize: 8192}}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	simple, ok := tr.(*simpleTransport)
	if !ok {
		t.Fatalf("transport type=%T, want *simpleTransport", tr)
	}
	if simple.client.ReadBufferSize != 8192 {
		t.Fatalf("read buffer size=%d, want 8192", simple.client.ReadBufferSize)
	}
}

func TestNew_HTTP3WithoutImpersonate(t *testing.T) {
	_, err := New(&config.Options{Stealth: config.StealthOptions{HTTP3: true}}, "")
	if err == nil {
		t.Fatal("expected error for --http3 without --impersonate")
	}
}

func TestNew_SimpleTransportWithProxy(t *testing.T) {
	tr, err := New(&config.Options{}, "http://proxy.local:8080")
	if err != nil {
		t.Fatalf("New with proxy: %v", err)
	}
	if tr.Name() != "fasthttp" {
		t.Errorf("name = %q, want fasthttp", tr.Name())
	}
}

func TestNew_SimpleTransportBadProxy(t *testing.T) {
	_, err := New(&config.Options{}, "://bad-url")
	if err == nil {
		t.Fatal("expected error for malformed proxy URL")
	}
}

func TestNew_TLSClientWithProxy(t *testing.T) {
	tr, err := New(&config.Options{
		Stealth: config.StealthOptions{Impersonate: "firefox"},
	}, "http://proxy.local:8080")
	if err != nil {
		t.Fatalf("New with proxy+impersonate: %v", err)
	}
	if tr.Name() != "tls-client" {
		t.Errorf("name = %q, want tls-client", tr.Name())
	}
}

func TestNew_TLSClientWithTimeout(t *testing.T) {
	tr, err := New(&config.Options{
		Stealth:    config.StealthOptions{Impersonate: "chrome"},
		Resilience: config.ResilienceOptions{Timeout: 5000},
	}, "")
	if err != nil {
		t.Fatalf("New with timeout: %v", err)
	}
	if tr.Name() != "tls-client" {
		t.Errorf("name = %q, want tls-client", tr.Name())
	}
}

func TestNew_TLSClientHTTP3Supported(t *testing.T) {
	tr, err := New(&config.Options{
		Stealth: config.StealthOptions{Impersonate: "chrome", HTTP3: true},
	}, "")
	if err != nil {
		t.Fatalf("New with http3+chrome: %v", err)
	}
	if tr.Name() != "tls-client" {
		t.Errorf("name = %q, want tls-client", tr.Name())
	}
}

func TestNew_TLSClientHTTP3UnsupportedProfile(t *testing.T) {
	_, err := New(&config.Options{
		Stealth: config.StealthOptions{Impersonate: "safari", HTTP3: true},
	}, "")
	if err == nil {
		t.Fatal("expected error for http3 on safari (not supported)")
	}
}

func TestNew_UnknownProfile(t *testing.T) {
	_, err := New(&config.Options{
		Stealth: config.StealthOptions{Impersonate: "netscape"},
	}, "")
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestNew_AllProfiles(t *testing.T) {
	for _, name := range []string{"chrome", "firefox", "safari", "edge"} {
		t.Run(name, func(t *testing.T) {
			tr, err := New(&config.Options{
				Stealth: config.StealthOptions{Impersonate: name},
			}, "")
			if err != nil {
				t.Fatalf("New(%q): %v", name, err)
			}
			if tr.Name() != "tls-client" {
				t.Errorf("name = %q, want tls-client", tr.Name())
			}
		})
	}
}

func TestSimpleTransport_Do(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	srv := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(200)
			ctx.SetBodyString("ok")
		},
	}
	go srv.Serve(ln) //nolint:errcheck

	tr := &simpleTransport{
		client: &fasthttp.Client{
			Dial: func(addr string) (net.Conn, error) { return nil, nil },
		},
		name: "test-fasthttp",
	}
	// Replace the dial func with the actual in-memory listener.
	tr.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	req.SetRequestURI("http://test/")
	if err := tr.Do(context.Background(), req, rsp); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if rsp.StatusCode() != 200 {
		t.Errorf("status = %d, want 200", rsp.StatusCode())
	}
	if string(rsp.Body()) != "ok" {
		t.Errorf("body = %q, want ok", rsp.Body())
	}
}

func TestSimpleTransport_DoWithDeadline(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	srv := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(204)
		},
	}
	go srv.Serve(ln) //nolint:errcheck

	tr := &simpleTransport{
		client: &fasthttp.Client{},
		name:   "test",
	}
	tr.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	req.SetRequestURI("http://test/")
	if err := tr.Do(ctx, req, rsp); err != nil {
		t.Fatalf("Do with deadline: %v", err)
	}
	if rsp.StatusCode() != 204 {
		t.Errorf("status = %d, want 204", rsp.StatusCode())
	}
}

func TestBuildTLSConfig_Insecure(t *testing.T) {
	cfg, err := buildTLSConfig(&config.Options{Stealth: config.StealthOptions{Insecure: true}})
	if err != nil {
		t.Fatalf("buildTLSConfig error: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify true")
	}
}

func TestBuildTLSConfig_None(t *testing.T) {
	cfg, err := buildTLSConfig(&config.Options{})
	if err != nil {
		t.Fatalf("buildTLSConfig error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil tls.Config when no TLS flags set, got %+v", cfg)
	}
}

func TestBuildTLSConfig_CertRequiresKey(t *testing.T) {
	cfg, err := buildTLSConfig(&config.Options{Stealth: config.StealthOptions{ClientCert: "somecert.pem"}})
	if err == nil {
		t.Fatal("expected error when --cert is set without --key")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on error, got %+v", cfg)
	}
}

func TestBuildTLSConfig_BadCACert(t *testing.T) {
	tmpdir := t.TempDir()
	tmpfile := tmpdir + "/bad.pem"
	if err := os.WriteFile(tmpfile, []byte("not a valid pem"), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	cfg, err := buildTLSConfig(&config.Options{Stealth: config.StealthOptions{CACert: tmpfile}})
	if err == nil {
		t.Fatal("expected error for invalid CA cert")
	}
	if cfg != nil {
		t.Fatalf("expected nil config on error, got %+v", cfg)
	}
}

func TestSimpleTransport_StreamWritesBodyToSink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "streamed-body")
	}))
	defer srv.Close()

	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(srv.URL)
	req.Header.SetMethod("GET")

	var buf bytes.Buffer
	meta, err := tr.Stream(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if meta.StatusCode != 200 {
		t.Errorf("status = %d, want 200", meta.StatusCode)
	}
	if !meta.AcceptRanges {
		t.Error("AcceptRanges = false, want true")
	}
	if buf.String() != "streamed-body" {
		t.Errorf("sink = %q, want streamed-body", buf.String())
	}
}

// TestSimpleTransport_StreamForwardsHeaders verifies Stream copies request
// headers (including Range) from the fasthttp request to the net/http request,
// and that a 206 + Last-Modified/Content-Disposition are surfaced in StreamMeta.
func TestSimpleTransport_StreamForwardsHeaders(t *testing.T) {
	var gotCustom, gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom")
		gotRange = r.Header.Get("Range")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("Content-Disposition", `attachment; filename="x.bin"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "partial")
	}))
	defer srv.Close()

	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(srv.URL)
	req.Header.SetMethod("GET")
	req.Header.Set("X-Custom", "value")
	req.Header.Set("Range", "bytes=0-6")

	var buf bytes.Buffer
	meta, err := tr.Stream(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if gotCustom != "value" {
		t.Errorf("server saw X-Custom = %q, want value", gotCustom)
	}
	if gotRange != "bytes=0-6" {
		t.Errorf("server saw Range = %q, want bytes=0-6", gotRange)
	}
	if meta.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", meta.StatusCode)
	}
	if meta.LastModified == "" || meta.Disposition == "" {
		t.Errorf("meta missing Last-Modified/Content-Disposition: %+v", meta)
	}
	if buf.String() != "partial" {
		t.Errorf("sink = %q, want partial", buf.String())
	}
}

// TestSimpleTransport_StreamConnectionError verifies Stream returns an error
// (rather than panicking) when the server is unreachable.
func TestSimpleTransport_StreamConnectionError(t *testing.T) {
	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://127.0.0.1:1/") // nothing listening on port 1
	req.Header.SetMethod("GET")

	if _, err := tr.Stream(context.Background(), req, io.Discard); err == nil {
		t.Fatal("expected an error streaming from an unreachable server")
	}
}
