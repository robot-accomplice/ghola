package proxy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// The client builds a new selector for each URL. Round-robin must still
// move to the next proxy for the next URL, or it never rotates and one
// proxy takes all the load.
func TestSelect_RoundRobinRotatesAcrossSelectors(t *testing.T) {
	list := "http://" + t.Name() + "-1.local:8080\nhttp://" + t.Name() + "-2.local:8080\n"
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 3 {
		sel, err := NewSelector("", "", path, "round-robin")
		if err != nil {
			t.Fatal(err)
		}
		p, _ := sel.Select()
		got = append(got, p)
	}
	if got[0] == got[1] || got[0] != got[2] {
		t.Errorf("one selector for each URL gave %q, want a rotation", got)
	}
}

// A proxy without a scheme is an HTTP proxy, and without a port it is on
// port 1080, as curl reads -x. v1.0.0 read "x" as a URL without a host and
// dialed ":0"; "host:3128" became the scheme "host".
func TestNewSelector_ProxyWithoutSchemeIsHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxies.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"x": "http://x:1080", "proxy.local:3128": "http://proxy.local:3128", "[::1]": "http://[::1]:1080"} {
		sel, err := NewSelector(in, "", "", "")
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got, _ := sel.Select(); got != want {
			t.Errorf("--proxy %s = %q, want %q", in, got, want)
		}
	}
	sel, err := NewSelector("", "", path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sel.Select(); got != "http://x:1080" {
		t.Errorf("--proxy-file line x = %q, want http://x:1080", got)
	}
}

// A proxy URL without a host cannot work. It is a bad option value
// (exit 2), not a dial error.
func TestNewSelector_RefusesAProxyWithoutAHost(t *testing.T) {
	for _, in := range []string{"http://", "socks5://:1080", "http://%zz"} {
		if _, err := NewSelector(in, "", "", ""); !errors.Is(err, config.ErrBadOption) {
			t.Errorf("--proxy %q: err = %v, want config.ErrBadOption", in, err)
		}
	}
}

// --proxy-user without ":" is a user with an empty password, as curl
// sends it when it cannot ask. It must not be dropped without a word.
func TestWithAuth_UserWithoutPassword(t *testing.T) {
	if got := withAuth("http://proxy.local:8080", "alice"); got != "http://alice@proxy.local:8080" {
		t.Errorf("withAuth = %q, want http://alice@proxy.local:8080", got)
	}
}
