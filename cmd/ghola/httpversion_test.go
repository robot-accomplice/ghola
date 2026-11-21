package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// protoServer records the protocol of each request it gets.
func protoServer(t *testing.T, start func(*httptest.Server)) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	last := ""
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Proto
		mu.Unlock()
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	start(ts)
	t.Cleanup(ts.Close)
	return ts, func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// --http picks the protocol on both stacks, and ghola curl maps curl's
// --http1.1 and --http2-prior-knowledge to it. A server that sees another
// protocol than the user asked for fails the test.
func TestExecute_HTTPVersion(t *testing.T) {
	tlsTS, tlsProto := protoServer(t, func(ts *httptest.Server) { ts.EnableHTTP2 = true; ts.StartTLS() })
	h2c, h2cProto := protoServer(t, func(ts *httptest.Server) {
		p := new(http.Protocols)
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		ts.Config.Protocols = p
		ts.Start()
	})
	for _, c := range []struct {
		args  []string
		proto func() string
		want  string
	}{
		{[]string{"-q", "-k", tlsTS.URL}, tlsProto, "HTTP/2.0"},
		{[]string{"-q", "-k", "--http", "1.1", tlsTS.URL}, tlsProto, "HTTP/1.1"},
		{[]string{"-q", "-k", "--impersonate", "chrome", tlsTS.URL}, tlsProto, "HTTP/2.0"},
		{[]string{"-q", "-k", "--impersonate", "chrome", "--http", "1.1", tlsTS.URL}, tlsProto, "HTTP/1.1"},
		{[]string{"curl", "-s", "-k", "--http1.1", tlsTS.URL}, tlsProto, "HTTP/1.1"},
		{[]string{"-q", h2c.URL}, h2cProto, "HTTP/1.1"},
		{[]string{"-q", "--http", "2-prior", h2c.URL}, h2cProto, "HTTP/2.0"},
		{[]string{"curl", "-s", "--http2-prior-knowledge", h2c.URL}, h2cProto, "HTTP/2.0"},
	} {
		if code := runGhola(t, c.args...); code != 0 {
			t.Errorf("%s: exit %d", strings.Join(c.args, " "), code)
			continue
		}
		if got := c.proto(); got != c.want {
			t.Errorf("%s: the server saw %s, want %s", strings.Join(c.args, " "), got, c.want)
		}
	}
	// 3-only fails when the answer does not come over HTTP/3, so a user
	// who asks for HTTP/3 never gets HTTP/2 without a sign.
	if runGhola(t, "-q", "-k", "--impersonate", "chrome", "--http", "3-only", "--timeout", "5s", "-r", "0", tlsTS.URL) == 0 {
		t.Error("--http 3-only against a server without HTTP/3: exit 0")
	}
	// QUIC dials past ghola's dial function, so --resolve would be
	// ignored without a sign: the pair is refused.
	stderr := captureStderr(t, func() {
		runGhola(t, "-q", "--impersonate", "chrome", "--http", "3", "--resolve", "h.example:443:127.0.0.1", "https://h.example/")
	})
	if !strings.Contains(stderr, "--http 3 does not work with --resolve") {
		t.Errorf("--http 3 with --resolve: %q", stderr)
	}
}
