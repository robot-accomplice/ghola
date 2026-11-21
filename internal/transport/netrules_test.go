package transport

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// hostRecorder records the Host header and the TLS server name of each
// request.
type hostRecorder struct {
	mu         sync.Mutex
	host, sni  string
	requestSaw bool
}

func (h *hostRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.host, h.requestSaw = r.Host, true
	if r.TLS != nil {
		h.sni = r.TLS.ServerName
	}
}

func port(ts *httptest.Server) string {
	return strconv.Itoa(ts.Listener.Addr().(*net.TCPAddr).Port)
}

// --resolve sends the connection to the address and keeps the name for
// Host and for the TLS server name, as curl does, on both stacks. The name
// does not resolve, so only the rule can make the request work.
func TestNetRules_Resolve(t *testing.T) {
	rec := &hostRecorder{}
	ts := httptest.NewTLSServer(rec)
	defer ts.Close()
	name := "ghola-test.invalid:" + port(ts)
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			opts := &config.Options{
				Stealth: config.StealthOptions{Impersonate: imp, Insecure: true},
				Net:     config.NetOptions{Resolve: []string{"GHOLA-TEST.invalid:" + port(ts) + ":127.0.0.1"}},
			}
			tr, err := New(opts, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := get(t, tr, "https://"+name+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.host != name || rec.sni != "ghola-test.invalid" {
				t.Errorf("Host %q, SNI %q; want the name of the URL", rec.host, rec.sni)
			}
		})
	}
}

// --connect-to sends a host:port to another host:port.
func TestNetRules_ConnectTo(t *testing.T) {
	rec := &hostRecorder{}
	ts := httptest.NewServer(rec)
	defer ts.Close()
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			opts := &config.Options{
				Stealth: config.StealthOptions{Impersonate: imp},
				Net:     config.NetOptions{ConnectTo: []string{"other.invalid:80:127.0.0.1:" + port(ts)}},
			}
			tr, _ := New(opts, "")
			if err := get(t, tr, "http://other.invalid/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if rec.host != "other.invalid" {
				t.Errorf("Host = %q", rec.host)
			}
		})
	}
}

// --ip 6 refuses an IPv4 address, and --ip 4 accepts it.
func TestNetRules_IPVersion(t *testing.T) {
	ts := httptest.NewServer(&hostRecorder{})
	defer ts.Close()
	for v, wantOK := range map[string]bool{"4": true, "6": false} {
		tr, _ := New(&config.Options{Net: config.NetOptions{IPVersion: v}}, "")
		err := get(t, tr, ts.URL+"/")
		if (err == nil) != wantOK {
			t.Errorf("--ip %s to an IPv4 server: err = %v", v, err)
		}
	}
}

// With --impersonate, the rules cannot go through an HTTP proxy: the
// error says so, and nothing goes out without the rules.
func TestNetRules_ImpersonateRefusesAnHTTPProxy(t *testing.T) {
	opts := &config.Options{Stealth: config.StealthOptions{Impersonate: "chrome"}, Net: config.NetOptions{Resolve: []string{"h:443:127.0.0.1"}}}
	if _, err := New(opts, "http://127.0.0.1:1"); !errors.Is(err, ErrRulesNeedDirect) {
		t.Fatalf("--proxy: err = %v", err)
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	tr, err := New(opts, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := get(t, tr, "http://h/"); !errors.Is(err, ErrRulesNeedDirect) {
		t.Fatalf("environment proxy: err = %v", err)
	}
}

// A malformed rule is an error.
func TestNetRules_BadRules(t *testing.T) {
	for _, n := range []config.NetOptions{
		{Resolve: []string{"h:443"}}, {Resolve: []string{"h:443:not-an-ip"}},
		{ConnectTo: []string{"h:443:x"}}, {IPVersion: "5"},
	} {
		if _, err := newNetRules(n); err == nil {
			t.Errorf("%+v was accepted", n)
		}
	}
}
