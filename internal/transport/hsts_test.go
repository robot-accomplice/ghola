package transport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// --hsts keeps the Strict-Transport-Security of an HTTPS answer in curl's
// file format, and then sends an http URL of that host (and, with
// includeSubDomains, of its subdomains) over https, on both stacks. A
// plain request would put cookies and credentials on the wire in clear
// text. max-age=0 removes the host, and an IP address is never kept.
func TestHSTS(t *testing.T) {
	var maxAge atomic.Value
	var overTLS atomic.Int32 // Go's TLS server answers plain HTTP itself, without the handler
	maxAge.Store("max-age=3600; includeSubDomains")
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		overTLS.Add(1)
		w.Header().Set("Strict-Transport-Security", maxAge.Load().(string))
	}))
	defer ts.Close()
	p := port(ts)
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			maxAge.Store("max-age=3600; includeSubDomains")
			cache := filepath.Join(t.TempDir(), "hsts.txt")
			newTr := func() Transport {
				tr, err := New(&config.Options{HSTS: cache, Stealth: config.StealthOptions{Impersonate: imp, Insecure: true},
					Net: config.NetOptions{Resolve: []string{"ghola-test.invalid:" + p + ":127.0.0.1", "sub.ghola-test.invalid:" + p + ":127.0.0.1"}}}, "")
				if err != nil {
					t.Fatal(err)
				}
				return tr
			}
			if err := get(t, newTr(), ts.URL+"/"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(cache); err == nil {
				t.Error("an IP address went into the cache")
			}
			if err := get(t, newTr(), "https://ghola-test.invalid:"+p+"/"); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(cache); !strings.Contains(string(b), `.ghola-test.invalid "`) {
				t.Fatalf("cache file:\n%s", b)
			}
			overTLS.Store(0)
			if err := get(t, newTr(), "http://sub.ghola-test.invalid:"+p+"/"); err != nil || overTLS.Load() != 1 {
				t.Errorf("http to a cached host was not sent over https: %v", err)
			}
			maxAge.Store("max-age=0")
			if err := get(t, newTr(), "https://ghola-test.invalid:"+p+"/"); err != nil {
				t.Fatal(err)
			}
			overTLS.Store(0)
			get(t, newTr(), "http://ghola-test.invalid:"+p+"/") //nolint:errcheck // plain HTTP to a TLS port
			if overTLS.Load() != 0 {
				t.Error("max-age=0 did not remove the host: the request still went over https")
			}
		})
	}
}
