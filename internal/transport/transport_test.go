package transport

import (
	"bytes"
	"context"
	"net"
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
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.SetBodyString("streamed-body")
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	tr := &simpleTransport{
		client: &fasthttp.Client{
			StreamResponseBody: true,
			Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
		},
		name: "fasthttp",
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://test/")
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
