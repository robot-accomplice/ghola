package transport

import (
	"net/url"
	"runtime"
	"testing"
)

// TestProxyFromEnv checks how the proxy variables apply (audit finding M8).
// Each case is a rule that a user of curl or wget expects.
func TestProxyFromEnv(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		target string
		want   string // "" for a direct connection
		cased  bool   // needs case-sensitive names: not on Windows
	}{
		{"http uses HTTP_PROXY", map[string]string{"HTTP_PROXY": "http://p:1"}, "http://a.example/", "http://p:1", false},
		{"lower case wins", map[string]string{"HTTP_PROXY": "http://up:1", "http_proxy": "http://low:1"}, "http://a.example/", "http://low:1", true},
		{"https uses HTTPS_PROXY", map[string]string{"HTTPS_PROXY": "http://p:2", "HTTP_PROXY": "http://p:1"}, "https://a.example/", "http://p:2", false},
		{"https does not use HTTP_PROXY", map[string]string{"HTTP_PROXY": "http://p:1"}, "https://a.example/", "", false},
		{"loopback is proxied, as in curl", map[string]string{"HTTP_PROXY": "http://p:1"}, "http://127.0.0.1:8080/", "http://p:1", false},
		{"no scheme means http", map[string]string{"HTTP_PROXY": "p:3128"}, "http://a.example/", "http://p:3128", false},
		{"CGI ignores HTTP_PROXY (httpoxy)", map[string]string{"HTTP_PROXY": "http://evil:1", "REQUEST_METHOD": "GET"}, "http://a.example/", "", true},
		{"NO_PROXY star", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "*"}, "http://a.example/", "", false},
		{"NO_PROXY host", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "x.test, a.example"}, "http://a.example/", "", false},
		{"NO_PROXY domain", map[string]string{"HTTP_PROXY": "http://p:1", "no_proxy": ".example"}, "http://b.a.example/", "", false},
		{"NO_PROXY suffix needs a dot", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "example"}, "http://badexample/", "http://p:1", false},
		{"NO_PROXY CIDR", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "10.0.0.0/8"}, "http://10.1.2.3/", "", false},
		{"NO_PROXY IPv6", map[string]string{"HTTP_PROXY": "http://p:1", "NO_PROXY": "::1"}, "http://[::1]:80/", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkProxyCase(t, tc.env, tc.target, tc.want, tc.cased) })
	}
}

// checkProxyCase sets env, and checks the proxy that proxyFromEnv gives for
// target.
func checkProxyCase(t *testing.T, env map[string]string, target, want string, cased bool) {
	t.Helper()
	if cased && runtime.GOOS == "windows" {
		t.Skip("Windows environment names are not case-sensitive")
	}
	// Clear all first, then set: on Windows HTTP_PROXY and
	// http_proxy are one variable.
	for _, n := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(n, "")
	}
	for n, v := range env {
		t.Setenv(n, v)
	}
	u, _ := url.Parse(target)
	p, err := proxyFromEnv(u)
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	if p != nil {
		got = p.String()
	}
	if got != want {
		t.Errorf("proxy = %q, want %q", got, want)
	}
}
