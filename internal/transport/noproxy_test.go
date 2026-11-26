package transport

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// no_proxy applies also with --proxy, as in curl: a host in it goes
// direct. v1.0.0 sent it to the proxy, so an internal host leaked to an
// outside proxy. The proxy here is dead, so only a direct request works.
func TestNoProxy_AppliesWithAFixedProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	for _, imp := range stacks {
		t.Setenv("no_proxy", "127.0.0.1")
		tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, deadProxy(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := get(t, tr, target.URL+"/"); err != nil {
			t.Errorf("impersonate=%q: a host in no_proxy went to the proxy: %v", imp, err)
		}
		t.Setenv("no_proxy", "")
		if err := get(t, tr, target.URL+"/"); err == nil {
			t.Errorf("impersonate=%q: the dead proxy was not used without no_proxy: the test proves nothing", imp)
		}
	}
}

// deadProxy returns a proxy URL on a port where nothing listens.
func deadProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
}
