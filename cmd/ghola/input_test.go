package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// --input reads the URLs from a file in aria2's format: an option line
// under a URL applies to that URL only (out= goes inside the run's -o
// DIR/, as in aria2). --input-format sitemap and html take the links of
// those files. A bad option line stops the run before any request.
func TestExecute_Input(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-A")
		mu.Unlock()
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lines := write("list.txt", srv.URL+"/a\n  out=first.txt\n# a comment\n"+srv.URL+"/b\n"+srv.URL+"/c\n\theader=X-A: only-c\n")
	out := filepath.Join(dir, "out") + "/"
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := runGhola(t, "download", "-q", "-j", "2", "-o", out, "--input", lines); code != 0 {
		t.Fatalf("lines: exit %d", code)
	}
	for f, want := range map[string]string{"first.txt": "/a", "b": "/b", "c": "/c"} {
		if b, _ := os.ReadFile(filepath.Join(out, f)); string(b) != want {
			t.Errorf("lines: %s = %q, want %q", f, b, want)
		}
	}
	if seen["/c"] != "only-c" || seen["/b"] != "" {
		t.Errorf("headers seen: %v; the option line belongs to /c only", seen)
	}
	sitemap := write("sitemap.xml", `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>`+srv.URL+`/s1</loc></url><url><loc>`+srv.URL+`/s2</loc></url></urlset>`)
	page := write("page.html", `<html><a href="`+srv.URL+`/h1">x</a><img src="`+srv.URL+`/h2"><a href="relative">y</a></html>`)
	for format, file := range map[string]string{"sitemap": sitemap, "html": page} {
		if code := runGhola(t, "download", "-q", "-o", out, "--input", file, "--input-format", format); code != 0 {
			t.Errorf("%s: exit %d", format, code)
		}
	}
	for _, p := range []string{"/s1", "/s2", "/h1", "/h2"} {
		if _, ok := seen[p]; !ok {
			t.Errorf("%s was not fetched", p)
		}
	}
	bad := write("bad.txt", srv.URL+"/never\n  no-such-option=1\n")
	if code := runGhola(t, "download", "-q", "-o", out, "--input", bad); code != 2 {
		t.Errorf("a bad option line: exit %d, want 2", code)
	}
	if _, ok := seen["/never"]; ok {
		t.Error("a request went out before the bad line stopped the run")
	}
}
