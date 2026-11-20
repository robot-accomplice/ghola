package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// --next starts the options for the next URL, as curl does: an option
// before it does not reach the URL after it. A failing group does not
// stop the next one, and the exit code is the last failure's. A typo in
// a later group stops the run before any request goes out.
func TestExecute_Next(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-A")
		mu.Unlock()
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if code := runGhola(t, "-q", "-H", "X-A: one", srv.URL+"/one", "--next", "-q", srv.URL+"/two"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if seen["/one"] != "one" || seen["/two"] != "" {
		t.Errorf("headers seen: %v; the -H of the first group must not reach the second", seen)
	}
	if code := runGhola(t, "-q", "-r", "0", srv.URL+"/missing", "--next", "-q", srv.URL+"/three"); code != 22 {
		t.Errorf("a failing first group: exit %d, want 22", code)
	}
	if _, ok := seen["/three"]; !ok {
		t.Error("the group after a failure did not run")
	}
	if code := runGhola(t, "-q", srv.URL+"/four", "--next", "--no-such-option", srv.URL+"/five"); code != 2 {
		t.Errorf("a typo in the second group: exit %d, want 2", code)
	}
	if _, ok := seen["/four"]; ok {
		t.Error("a request went out before the typo stopped the run")
	}
	if code := runGhola(t, "download", srv.URL+"/a", "--next", srv.URL+"/b"); code != 2 {
		t.Errorf("--next with download: exit %d, want 2", code)
	}
}

// ghola curl splits at curl's -: and --next: each group translates on its
// own, so a header of the first URL does not reach the second.
func TestExecute_CurlNext(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-A")
		mu.Unlock()
	}))
	defer srv.Close()
	if code := runGhola(t, "curl", "-s", "-H", "X-A: one", srv.URL+"/one", "-:", "-s", srv.URL+"/two"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if v, ok := seen["/two"]; seen["/one"] != "one" || !ok || v != "" {
		t.Errorf("headers seen: %v", seen)
	}
}
