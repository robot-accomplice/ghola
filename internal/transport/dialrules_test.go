package transport

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// socksGet fetches url through a SOCKS proxy of scheme with the net
// options n, and returns how the proxy saw the target ("ip" or "name").
func socksGet(t *testing.T, imp, scheme string, n config.NetOptions, url string) (string, error) {
	t.Helper()
	srv := newSocksServer(t, "")
	tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: n}, scheme+"://"+srv.ln.Addr().String())
	if err != nil {
		return "", err
	}
	err = get(t, tr, url)
	_, naming, _ := srv.last()
	return naming, err
}

// socks4 and socks5 resolve the name in ghola, so --resolve and --doh must
// apply there. v1.0.0 asked the system DNS, which ignored the rule and
// leaked the name outside DoH. The name does not resolve in the system.
func TestSOCKS_LocalResolutionUsesResolveAndDoH(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	var asked atomic.Int32
	doh := dohServer(t, &asked)
	url := "http://ghola-test.invalid:" + port(target) + "/"
	for _, imp := range stacks {
		for name, n := range map[string]config.NetOptions{
			"--resolve": {Resolve: []string{"ghola-test.invalid:" + port(target) + ":127.0.0.1"}},
			"--doh":     {DoH: doh.URL},
		} {
			for _, scheme := range []string{"socks4", "socks5"} {
				naming, err := socksGet(t, imp, scheme, n, url)
				if err != nil || naming != "ip" {
					t.Errorf("%s %s impersonate=%q: naming %q, err %v; want the rule's address", scheme, name, imp, naming, err)
				}
			}
		}
	}
}

// socks5h sends the name to the proxy, so --connect-to must change that
// name: the proxy then connects to the rule's target.
func TestSOCKS_ProxyResolutionUsesConnectTo(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	n := config.NetOptions{ConnectTo: []string{"other.invalid:80:localhost:" + port(target)}}
	for _, imp := range stacks {
		for _, scheme := range []string{"socks4a", "socks5h"} {
			if _, err := socksGet(t, imp, scheme, n, "http://other.invalid/"); err != nil {
				t.Errorf("%s impersonate=%q: %v", scheme, imp, err)
			}
		}
	}
}

// --resolve takes several addresses and tries each in turn, as curl does:
// the first one here has no server.
func TestNetRules_ResolveTriesEachAddress(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	dead := deadAddr(t)
	for _, imp := range stacks {
		rule := "ghola-test.invalid:" + port(target) + ":[" + dead + "],127.0.0.1"
		tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: config.NetOptions{Resolve: []string{rule}}}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := get(t, tr, "http://ghola-test.invalid:"+port(target)+"/"); err != nil {
			t.Errorf("impersonate=%q: %v", imp, err)
		}
	}
}

// deadAddr is an address of this host where the test server does not
// listen: the server listens on 127.0.0.1 only, so ::1 on its port refuses
// (or has no route without IPv6). Both fail at once on every OS.
func deadAddr(t *testing.T) string {
	t.Helper()
	return "::1"
}

// The curl forms of --resolve: "*" for any name, a "+" prefix (an entry
// that times out in curl), and "-host:port", which removes an entry.
func TestNetRules_ResolveCurlForms(t *testing.T) {
	r, err := newNetRules(config.NetOptions{Resolve: []string{
		"*:443:10.0.0.1", "+a.test:443:10.0.0.2", "b.test:443:10.0.0.3", "-b.test:443",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]string{"x.test:443": "10.0.0.1", "a.test:443": "10.0.0.2", "b.test:443": "10.0.0.1", "x.test:80": ""} {
		got := ""
		if ips := r.addresses(addr); len(ips) > 0 {
			got = ips[0]
		}
		if got != want {
			t.Errorf("%s resolves to %q, want %q", addr, got, want)
		}
	}
}

// The curl forms of --connect-to: an empty source host or port matches
// any, and an empty target host or port keeps the one of the URL.
func TestNetRules_ConnectToCurlForms(t *testing.T) {
	r, err := newNetRules(config.NetOptions{ConnectTo: []string{"::proxy.test:8080", "a.test:443::8443"}})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]string{"a.test:443": "proxy.test:8080", "b.test:80": "proxy.test:8080"} {
		if got := r.connect(addr); got != want {
			t.Errorf("%s connects to %q, want %q", addr, got, want)
		}
	}
	r, _ = newNetRules(config.NetOptions{ConnectTo: []string{"a.test:443::8443"}})
	if got := r.connect("a.test:443"); got != "a.test:8443" {
		t.Errorf("an empty target host: %q, want a.test:8443", got)
	}
}

// A bad --resolve, --connect-to, --ip or --local-port value is an
// argument error (exit 2), not a transfer error.
func TestNetRules_BadValuesAreBadOptions(t *testing.T) {
	for _, n := range []config.NetOptions{
		{Resolve: []string{"h:443"}}, {Resolve: []string{"h:443:not-an-ip"}},
		{ConnectTo: []string{"h:443:x"}}, {IPVersion: "5"}, {LocalPort: "70000"},
	} {
		if _, err := New(&config.Options{Net: n}, ""); !errors.Is(err, config.ErrBadOption) {
			t.Errorf("%+v: err = %v, want config.ErrBadOption", n, err)
		}
	}
}

// --interface NAME must use the address of the target's family: an IPv4
// source cannot reach an IPv6 target. v1.0.0 took the first address. A
// name target tries one source of each family, IPv4 first.
func TestInterface_TakesTheAddressOfTheTargetFamily(t *testing.T) {
	v6 := net.ParseIP("fe80::1")
	v4 := net.ParseIP("192.0.2.7")
	addrs := []net.IP{v6, v4}
	for _, tc := range []struct {
		network, target string
		want            []net.IP
	}{
		{"tcp", "198.51.100.1:80", []net.IP{v4}},
		{"tcp", "[2001:db8::1]:80", []net.IP{v6}},
		{"tcp6", "name.test:80", []net.IP{v6}},
		{"tcp", "name.test:80", []net.IP{v4, v6}},
	} {
		got := sourcesFor(addrs, tc.network, tc.target)
		if len(got) != len(tc.want) || !got[0].Equal(tc.want[0]) {
			t.Errorf("%s %s: sources %v, want %v", tc.network, tc.target, got, tc.want)
		}
	}
}

// --resolve, --connect-to and --ip apply to FTP and WebSocket too, as in
// curl. v1.0.0 dialed these protocols without the rules. The name does
// not resolve in the system, so only the rule can make the transfer work.
// The FTP data connection goes to the address of the control connection.
func TestNetRules_ApplyToFTPAndWebSocket(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	_, ftpPort, _ := net.SplitHostPort(srv.addr())
	n := config.NetOptions{Resolve: []string{"ghola-test.invalid:" + ftpPort + ":127.0.0.1"}}
	rsp, err := ftpDo(t, &config.Options{Net: n}, "", "GET", "ftp://ghola-test.invalid:"+ftpPort+"/pub/a.txt", "", nil)
	if err != nil {
		t.Fatalf("ftp with --resolve: %v", err)
	}
	if got := readFTP(t, rsp); got != "0123456789" {
		t.Errorf("ftp body %q", got)
	}
	n = config.NetOptions{ConnectTo: []string{"other.invalid:21:127.0.0.1:" + ftpPort}}
	if _, err := ftpDo(t, &config.Options{Net: n}, "", "GET", "ftp://other.invalid/pub/a.txt", "", nil); err != nil {
		t.Errorf("ftp with --connect-to: %v", err)
	}
	for _, imp := range stacks {
		s := &wsServer{}
		ts := httptest.NewServer(s.handler(false))
		n := config.NetOptions{Resolve: []string{"ghola-test.invalid:" + port(ts) + ":127.0.0.1"}}
		opts := &config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: n}
		if got, err := wsGet(t, opts, "", "ws://ghola-test.invalid:"+port(ts)+"/feed", "", nil); err != nil || got != "hello bye" {
			t.Errorf("ws with --resolve, impersonate=%q: %q, %v", imp, got, err)
		}
		ts.Close()
	}
}
