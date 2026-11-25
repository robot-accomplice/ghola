package proxy

import (
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
)

// captureStderr returns what fn writes to stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = orig
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// --proxy-check names each proxy on stderr. The --proxy-user password is
// part of the proxy URL by then, and it must not reach the terminal or a
// log. Before, the line held "http://pu:SECRET@host".
func TestHealthCheck_HidesTheProxyPassword(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s, err := NewSelector("http://"+l.Addr().String(), "pu:SECRETPP", "", "")
	if err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() { s.HealthCheck() }) //nolint:errcheck
	if strings.Contains(out, "SECRETPP") {
		t.Errorf("stderr shows the password: %q", out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("stderr %q, want an OK line", out)
	}
}

// A SOCKS proxy without a port listens on 1080, as curl assumes. Before,
// the check tried port 80.
func TestCheckAddr_SOCKSDefaultPort(t *testing.T) {
	for raw, want := range map[string]string{"socks5://p": "p:1080", "socks5h://p": "p:1080", "socks4://p": "p:1080", "http://p": "p:80", "https://p": "p:443", "http://p:3128": "p:3128"} {
		if got := checkAddr(mustParse(t, raw)); got != want {
			t.Errorf("%s: %s, want %s", raw, got, want)
		}
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
