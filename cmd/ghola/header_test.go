package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestExecute_HeaderRemoval checks that -H "Name:" removes the header, as in
// curl (audit finding M7). Before, -H "User-Agent:" let the HTTP engine send
// its own User-Agent (Go-http-client/1.1), which names the library ghola
// uses. Both stacks are checked: net/http, and tls-client for --impersonate.
func TestExecute_HeaderRemoval(t *testing.T) {
	got := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
	}))
	defer srv.Close()

	for _, extra := range [][]string{nil, {"--impersonate", "chrome"}} {
		args := append(append([]string{"-q", "-H", "User-Agent:", "-H", "Accept:"}, extra...), srv.URL)
		out, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		if code := execute(context.Background(), args, nil, nil, out); code != 0 {
			t.Fatalf("ghola %q exit = %d", args, code)
		}
		out.Close()
		h := <-got
		if v, ok := h["Accept"]; ok {
			t.Errorf("ghola %q sent Accept: %q, want no Accept header", args, v)
		}
		ua, sent := h["User-Agent"]
		switch {
		case extra == nil && sent:
			t.Errorf("ghola %q sent User-Agent: %q, want no User-Agent header", args, ua)
		case sent && (len(ua) != 1 || ua[0] != ""):
			// fhttp (tls-client) over HTTP/1 writes the empty header that
			// stops its default. It sends no value, so it names no library.
			t.Errorf("ghola %q sent User-Agent: %q, want no value", args, ua)
		}
	}
}
