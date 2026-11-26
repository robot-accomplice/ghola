package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A name that DoH does not resolve must exit 6, as in curl. The DoH
// query ran with the transfer's context, so its connection counted as the
// transfer's connection and the error became a receive error (exit 56).
func TestDoH_NoAnswerIsNotAReceiveError(t *testing.T) {
	var asked atomic.Int32
	doh := dohServer(t, &asked)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	for _, imp := range stacks {
		tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: config.NetOptions{DoH: doh.URL}}, "")
		if err != nil {
			t.Fatal(err)
		}
		err = get(t, tr, "http://other.invalid:"+port(target)+"/")
		if !errors.Is(err, ErrNoAddress) || errors.Is(err, ErrRecv) {
			t.Errorf("impersonate=%q: err = %v, want ErrNoAddress and not ErrRecv", imp, err)
		}
	}
}

// An https DoH server is verified with the transfer's trust: --tls-ca
// names the CA of a private resolver, and -k turns the check off. v1.0.0
// used the system store only, so a private DoH server could not work.
func TestDoH_UsesTheTLSCAAndInsecure(t *testing.T) {
	var asked atomic.Int32
	plain := dohServer(t, &asked)
	p := newTestPKI(t)
	doh := p.server(t, nil)
	doh.Config.Handler = plain.Config.Handler
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	url := "http://ghola-test.invalid:" + port(target) + "/"
	try := func(st config.StealthOptions) error {
		tr, err := New(&config.Options{Stealth: st, Net: config.NetOptions{DoH: doh.URL}}, "")
		if err != nil {
			return err
		}
		return get(t, tr, url)
	}
	if err := try(config.StealthOptions{}); err == nil {
		t.Fatal("the DoH server is trusted without --tls-ca: the test proves nothing")
	}
	if err := try(config.StealthOptions{CACert: p.caFile}); err != nil {
		t.Errorf("--tls-ca: %v", err)
	}
	if err := try(config.StealthOptions{Insecure: true}); err != nil {
		t.Errorf("-k: %v", err)
	}
}
