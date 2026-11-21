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

// The --proxy-tls-* options set the TLS of an https proxy, apart from the
// server's: the proxy's own CA, or --no-proxy-tls-verify. Without them
// the proxy certificate must verify, so a proxy that is not trusted
// sees no request.
func TestProxyTLS(t *testing.T) {
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxied:" + r.URL.String())) //nolint:errcheck
	}))
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "proxy-ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0o600) //nolint:errcheck
	fetch := func(st config.StealthOptions) (string, error) {
		tr, err := New(&config.Options{ProxyCfg: config.ProxyOptions{TLS: st}}, proxy.URL)
		if err != nil {
			return "", err
		}
		req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)
		req.SetRequestURI("http://ghola-test.invalid/x")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tr.Do(ctx, req, rsp); err != nil {
			return "", err
		}
		b, err := ReadBody(rsp)
		return string(b), err
	}
	for name, st := range map[string]config.StealthOptions{"--proxy-tls-ca": {CACert: ca}, "--no-proxy-tls-verify": {Insecure: true}} {
		if got, err := fetch(st); err != nil || got != "proxied:http://ghola-test.invalid/x" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if _, err := fetch(config.StealthOptions{}); err == nil {
		t.Error("an untrusted proxy certificate was accepted")
	}
	if _, err := New(&config.Options{ProxyCfg: config.ProxyOptions{TLS: config.StealthOptions{CACert: ca}}}, ""); err == nil {
		t.Error("--proxy-tls-ca without an https proxy was accepted")
	}
}
