package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/cli"
)

// seenRequest is what a test server saw of one request.
type seenRequest struct {
	method, path, body string
	header             http.Header
}

func recordingServer(t *testing.T) (*httptest.Server, func() []seenRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []seenRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, seenRequest{r.Method, r.URL.RequestURI(), string(b), r.Header.Clone()})
		mu.Unlock()
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	return ts, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

// "Copy as cURL" from Chrome, Firefox and Safari runs unchanged with
// ghola curl, and the server gets the headers, the cookies and the body
// (plan section 6). The commands have the shape each browser writes.
func TestExecute_CopyAsCurl(t *testing.T) {
	ts, seen := recordingServer(t)
	u := ts.URL + "/api?q=1"
	for browser, args := range map[string][]string{
		"chrome": {u, "-H", "accept: application/json", "-H", "accept-language: en-US,en;q=0.9",
			"-b", "session=abc; theme=dark", "-H", "sec-ch-ua-mobile: ?0", "-H", "user-agent: Mozilla/5.0 Chrome/131.0",
			"--data-raw", `{"a":1}`, "--compressed"},
		"firefox": {u, "-X", "POST", "-H", "User-Agent: Mozilla/5.0 Firefox/133.0", "-H", "Accept: */*",
			"-H", "Content-Type: application/json", "-H", "Origin: " + ts.URL, "-H", "Connection: keep-alive",
			"-H", "Cookie: session=abc", "--data-raw", `{"a":1}`},
		"safari": {u, "-X", "POST", "-H", "Content-Type: application/json", "-H", "Accept: */*",
			"-H", "User-Agent: Mozilla/5.0 Safari/605.1.15", "-H", "Cookie: session=abc", "--data-binary", `{"a":1}`},
	} {
		before := len(seen())
		if code := runGhola(t, append([]string{"curl", "-s"}, args...)...); code != 0 {
			t.Errorf("%s: exit %d", browser, code)
			continue
		}
		all := seen()
		if len(all) != before+1 {
			t.Errorf("%s: %d requests", browser, len(all)-before)
			continue
		}
		r := all[len(all)-1]
		if r.method != "POST" || r.path != "/api?q=1" || r.body != `{"a":1}` || !strings.Contains(r.header.Get("Cookie"), "session=abc") {
			t.Errorf("%s: %s %s body %q cookie %q", browser, r.method, r.path, r.body, r.header.Get("Cookie"))
		}
	}
}

// The command that --show-native prints sends the same request as the
// ghola curl command it came from (plan section 6): the method, the
// path, the body and the headers that the user gave.
func TestExecute_ShowNativeIsTheSame(t *testing.T) {
	ts, seen := recordingServer(t)
	curlArgs := []string{"-s", "-X", "PUT", "-H", "X-A: 1", "-u", "u:p", "--data-raw", "body", "-A", "agent/1", ts.URL + "/x"}
	native, err := cli.FromCurl(curlArgs)
	if err != nil {
		t.Fatal(err)
	}
	if code := runGhola(t, append([]string{"curl"}, curlArgs...)...); code != 0 {
		t.Fatalf("ghola curl: exit %d", code)
	}
	if code := runGhola(t, native.Args...); code != 0 {
		t.Fatalf("ghola %v: exit %d", native.Args, code)
	}
	all := seen()
	if len(all) != 2 {
		t.Fatalf("%d requests", len(all))
	}
	a, b := all[0], all[1]
	for _, h := range []string{"X-A", "Authorization", "User-Agent", "Content-Type"} {
		if a.header.Get(h) != b.header.Get(h) {
			t.Errorf("%s: %q and %q", h, a.header.Get(h), b.header.Get(h))
		}
	}
	if a.method != b.method || a.path != b.path || a.body != b.body {
		t.Errorf("curl %s %s %q, native %s %s %q", a.method, a.path, a.body, b.method, b.path, b.body)
	}
}

// --impersonate gives the browser's TLS Client Hello in every mode:
// fetch, download and mirror (plan section 6). A browser profile sends
// GREASE cipher values (0x?A?A), which Go's own TLS never sends, so the
// server can tell them apart.
func TestExecute_ImpersonateInEveryMode(t *testing.T) {
	var mu sync.Mutex
	grease := map[bool]int{}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>page</html>") //nolint:errcheck
	}))
	ts.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		has := false
		for _, c := range h.CipherSuites {
			has = has || c&0x0f0f == 0x0a0a
		}
		mu.Lock()
		grease[has]++
		mu.Unlock()
		return nil, nil
	}}
	ts.StartTLS()
	defer ts.Close()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"-q", "-k", "--impersonate", "chrome", ts.URL + "/"},
		{"download", "-q", "-k", "--impersonate", "chrome", "-o", filepath.Join(dir, "f"), ts.URL + "/f"},
		{"mirror", "-q", "-k", "--impersonate", "chrome", "--no-robots", "--depth", "1", "-o", dir + "/", ts.URL + "/"},
	} {
		if code := runGhola(t, args...); code != 0 {
			t.Errorf("%s: exit %d", args[0], code)
		}
	}
	mu.Lock()
	browser, plain := grease[true], grease[false]
	mu.Unlock()
	if browser < 3 || plain != 0 {
		t.Errorf("Client Hellos: %d with GREASE, %d without; every mode must send the browser's", browser, plain)
	}
	if runGhola(t, "-q", "-k", ts.URL+"/") != 0 {
		t.Fatal("plain fetch failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if grease[false] != 1 {
		t.Error("without --impersonate the Client Hello has GREASE: the check proves nothing")
	}
}
