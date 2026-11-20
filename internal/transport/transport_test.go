package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

func TestNewSelectsSimpleTransportWithoutImpersonation(t *testing.T) {
	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tr.Name() != "net/http" {
		t.Fatalf("transport name=%q, want net/http", tr.Name())
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

// --buffer-size can raise the response header limit, so a server with very
// large headers still works. A small value must not lower net/http's default
// limit: the v0 default (4096) would make normal large headers fail.
func TestNewHeaderLimitFromBufferSize(t *testing.T) {
	for _, tc := range []struct {
		bufferSize int
		want       int64
	}{
		{0, http.DefaultMaxHeaderBytes},
		{config.DefaultBufferSize, http.DefaultMaxHeaderBytes},
		{4 << 20, 4 << 20},
	} {
		tr, err := New(&config.Options{Resilience: config.ResilienceOptions{BufferSize: tc.bufferSize}}, "")
		if err != nil {
			t.Fatalf("New returned error: %v", err)
		}
		simple, ok := tr.(fileTransport).network.(*simpleTransport) // New puts file:// in front
		if !ok {
			t.Fatalf("transport type=%T, want *simpleTransport", tr)
		}
		got := simple.httpClient.Transport.(*http.Transport).MaxResponseHeaderBytes
		if got != tc.want {
			t.Errorf("buffer size %d: header limit=%d, want %d", tc.bufferSize, got, tc.want)
		}
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
	if tr.Name() != "net/http" {
		t.Errorf("name = %q, want net/http", tr.Name())
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

// Do and Stream must send the same request: when they used different
// engines, -u reached only one of them (audit finding H8).
func TestSimpleTransport_DoAndStreamSendTheSameRequest(t *testing.T) {
	type seen struct{ method, host, auth, body string }
	got := make(chan seen, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.Host, r.Header.Get("Authorization"), string(b)}
		w.Header().Add("Set-Cookie", "a=1")
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	newReq := func() *fasthttp.Request {
		req := fasthttp.AcquireRequest()
		req.SetRequestURI(srv.URL + "/")
		req.Header.SetMethod(fasthttp.MethodPost)
		req.Header.SetHost("example.test")
		req.Header.Set("Authorization", "Basic YTpi")
		req.SetBodyString("payload")
		return req
	}
	want := seen{"POST", "example.test", "Basic YTpi", "payload"}

	req, rsp := newReq(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	if err := tr.Do(context.Background(), req, rsp); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if rsp.StatusCode() != 200 || string(rsp.Body()) != "ok" {
		t.Errorf("Do response = %d %q, want 200 \"ok\"", rsp.StatusCode(), rsp.Body())
	}
	if c := rsp.Header.PeekCookie("a"); len(c) == 0 {
		t.Error("Do response lost the Set-Cookie header")
	}
	if g := <-got; g != want {
		t.Errorf("Do sent %+v, want %+v", g, want)
	}

	req2 := newReq()
	defer fasthttp.ReleaseRequest(req2)
	var sink bytes.Buffer
	if _, err := tr.Stream(context.Background(), req2, &sink); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if g := <-got; g != want {
		t.Errorf("Stream sent %+v, want %+v", g, want)
	}
}

// The context deadline is the request timeout on the Do path.
func TestSimpleTransport_DoHonoursDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	req.SetRequestURI(srv.URL + "/")
	start := time.Now()
	if err := tr.Do(ctx, req, rsp); err == nil {
		t.Fatal("Do returned no error after the deadline")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Do returned after %v, want about 100ms", d)
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
