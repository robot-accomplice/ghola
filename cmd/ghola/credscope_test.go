package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// authByHost records the Authorization header that each Host got.
func authByHost(t *testing.T) (*httptest.Server, func() map[string]string) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.Host] = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

// Credentials belong to one host. In a run with several URLs, -u and
// --auth-bearer go only to the host of the first URL (spec decision,
// 2026-11-25), and netrc gives each host its own entry or none. Before,
// the netrc entry of the first host went to every URL.
func TestExecute_CredentialsStayWithTheirHost(t *testing.T) {
	srv, got := authByHost(t)
	port := mustURLPort(t, srv.URL)
	first := "127.0.0.1:" + port
	other := "other.test:" + port
	resolve := "other.test:" + port + ":127.0.0.1"
	netrc := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(netrc, []byte("machine 127.0.0.1 login nuser password FIRSTHOST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"netrc", []string{"--auth-netrc-file", netrc}},
		{"-u", []string{"-u", "u:FIRSTHOST"}},
		{"bearer", []string{"--auth-bearer", "FIRSTHOST"}},
	} {
		args := append(append([]string{"-q", "-r", "0", "--resolve", resolve}, tc.args...), "http://"+first+"/a", "http://"+other+"/b")
		if code := runGhola(t, args...); code != 0 {
			t.Fatalf("%s: exit %d", tc.name, code)
		}
		h := got()
		if h[first] == "" {
			t.Errorf("%s: the first host got no credentials", tc.name)
		}
		if h[other] != "" {
			t.Errorf("%s: the second host got %q", tc.name, h[other])
		}
	}
}

func mustURLPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}
