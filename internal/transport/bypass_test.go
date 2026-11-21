package transport

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// counter counts the requests that reach it.
type counter struct{ n atomic.Int32 }

func (c *counter) ServeHTTP(http.ResponseWriter, *http.Request) { c.n.Add(1) }

// --proxy-bypass sends a listed host direct, with --proxy and with a proxy
// from the environment, on both stacks. Set, it replaces no_proxy, as
// curl --noproxy does: an empty list then proxies a host that no_proxy
// lists. A host that should go direct must not reach the proxy, which
// could log it.
func TestProxyBypass(t *testing.T) {
	target, proxy := &counter{}, &counter{}
	ts, ps := httptest.NewServer(target), httptest.NewServer(proxy)
	defer ts.Close()
	defer ps.Close()
	host, none := "127.0.0.1", ""
	tests := []struct {
		name      string
		fixed     string
		env       map[string]string
		bypass    *string
		wantProxy bool
	}{
		{"--proxy", ps.URL, nil, nil, true},
		{"--proxy, bypassed", ps.URL, nil, &host, false},
		{"environment, bypassed", "", map[string]string{"HTTP_PROXY": ps.URL}, &host, false},
		{"environment, the list replaces no_proxy", "", map[string]string{"HTTP_PROXY": ps.URL, "NO_PROXY": host}, &none, true},
		{"environment, no_proxy", "", map[string]string{"HTTP_PROXY": ps.URL, "NO_PROXY": host}, nil, false},
	}
	for _, imp := range stacks {
		for _, tc := range tests {
			t.Run(tc.name+", impersonate="+imp, func(t *testing.T) {
				setProxyEnv(t, tc.env)
				target.n.Store(0)
				proxy.n.Store(0)
				opts := &config.Options{Stealth: config.StealthOptions{Impersonate: imp}, ProxyCfg: config.ProxyOptions{Bypass: tc.bypass}}
				tr, err := New(opts, tc.fixed)
				if err != nil {
					t.Fatal(err)
				}
				// The counter is no real proxy: tls-client's CONNECT through it
				// fails after the proxy saw it.
				if err := get(t, tr, ts.URL+"/"); err != nil && !tc.wantProxy {
					t.Fatalf("GET: %v", err)
				}
				if (proxy.n.Load() == 1 && target.n.Load() == 0) != tc.wantProxy {
					t.Errorf("proxy %d, target %d requests; want through the proxy: %v", proxy.n.Load(), target.n.Load(), tc.wantProxy)
				}
			})
		}
	}
}

// setProxyEnv sets the proxy variables for the test, and clears the
// others first: on Windows, HTTP_PROXY and http_proxy are one name.
func setProxyEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"http_proxy", "no_proxy", "all_proxy", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}
