package transport

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// -k and --tls-ca are for the server, not for an https proxy: the proxy
// has its own --proxy-tls-* options, as curl has --proxy-insecure and
// --proxy-cacert. Before, -k also turned off the proxy check on the
// default stack, so Proxy-Authorization went over a TLS link that ghola
// did not check.
func TestProxyTLS_ServerOptionsDoNotCoverTheProxy(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxied")) //nolint:errcheck
	}))
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "proxy-ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0o600) //nolint:errcheck
	for name, st := range map[string]config.StealthOptions{"-k": {Insecure: true}, "--tls-ca": {CACert: ca}} {
		tr, err := New(&config.Options{Stealth: st}, proxy.URL)
		if err != nil {
			t.Fatal(err)
		}
		req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		req.SetRequestURI("http://ghola-test.invalid/x")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = tr.Do(ctx, req, rsp)
		cancel()
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		if err == nil {
			t.Errorf("%s: the untrusted proxy certificate was accepted", name)
		}
	}
}

// The same rule holds for an https proxy that HTTPS_PROXY names.
func TestProxyTLS_ServerOptionsDoNotCoverTheEnvProxy(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxied")) //nolint:errcheck
	}))
	defer proxy.Close()
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(k, "")
	}
	t.Setenv("HTTP_PROXY", proxy.URL)
	tr, err := New(&config.Options{Stealth: config.StealthOptions{Insecure: true}}, "")
	if err != nil {
		t.Fatal(err)
	}
	req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	req.SetRequestURI("http://ghola-test.invalid/x")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tr.Do(ctx, req, rsp); err == nil {
		t.Error("-k: the untrusted certificate of the environment proxy was accepted")
	}
}
