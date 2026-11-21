package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// siteServer is a small site under /site/, with a robots.txt that
// disallows /site/private/. It records the paths asked for and the
// If-Modified-Since of each.
func siteServer(t *testing.T) (*httptest.Server, func() map[string]string) {
	t.Helper()
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pages := map[string]string{
		"/robots.txt": "User-agent: *\nDisallow: /site/private/\n",
		"/site/": `<html><a href="a.html">a</a> <a href="sub/b.html">b</a> <img src="img.png">
<link rel="stylesheet" href="style.css"> <a href="/outside.html">up</a> <a href="private/p.html">p</a>
<a href="big.iso">iso</a> <a rel="nofollow" href="nf.html">nf</a> <a href="http://other.invalid/x">off</a></html>`,
		"/site/a.html":         `<a href="deep1.html">d1</a>`,
		"/site/deep1.html":     `<a href="deep2.html">d2</a>`,
		"/site/deep2.html":     `end`,
		"/site/sub/b.html":     `<a href="../a.html">back</a>`,
		"/site/img.png":        "PNG",
		"/site/style.css":      `body { background: url("bg.png") }`,
		"/site/bg.png":         "BG",
		"/site/big.iso":        "ISO",
		"/site/nf.html":        `<a href="missing.html">broken</a>`, // only --robots=false follows it
		"/site/private/p.html": "private",
		"/outside.html":        "outside",
	}
	types := map[string]string{".png": "image/png", ".css": "text/css", ".iso": "application/octet-stream", ".txt": "text/plain"}
	var mu sync.Mutex
	asked := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Path] = r.Header.Get("If-Modified-Since")
		mu.Unlock()
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		ct := types[filepath.Ext(r.URL.Path)]
		if ct == "" {
			ct = "text/html"
		}
		w.Header().Set("Content-Type", ct)
		http.ServeContent(w, r, "", modified, strings.NewReader(body))
	}))
	t.Cleanup(ts.Close)
	return ts, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range asked {
			out[k] = v
		}
		return out
	}
}

// ghola mirror follows the links of a site within its rules, as wget -m
// does: the depth, the start folder, robots.txt, rel=nofollow, --reject,
// and the url() of a style sheet. --convert-links makes the copy work
// offline; --spider saves nothing; --timestamps asks with
// If-Modified-Since and keeps a file that did not change.
func TestExecute_Mirror(t *testing.T) {
	ts, asked := siteServer(t)
	root := t.TempDir() + "/"
	args := []string{"mirror", "-q", "--layout", "no-host", "--depth", "2", "--reject", "*.iso", "--convert-links", "-o", root, ts.URL + "/site/"}
	if code := runGhola(t, args...); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"site/index.html", "site/a.html", "site/sub/b.html", "site/deep1.html", "site/img.png", "site/style.css", "site/bg.png"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s was not saved", f)
		}
	}
	for _, f := range []string{"site/deep2.html", "outside.html", "site/private/p.html", "site/big.iso", "site/nf.html"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("%s was saved; a rule should keep it out", f)
		}
	}
	for _, p := range []string{"/site/big.iso", "/site/private/p.html"} {
		if _, ok := asked()[p]; ok {
			t.Errorf("%s was fetched", p)
		}
	}
	index, _ := os.ReadFile(filepath.Join(root, "site/index.html"))
	if !strings.Contains(string(index), `href="a.html"`) || !strings.Contains(string(index), `href="`+ts.URL+`/outside.html"`) {
		t.Errorf("--convert-links: the index is\n%s", index)
	}
	if code := runGhola(t, args...); code != 0 {
		t.Fatalf("second run: exit %d", code)
	}
	if asked()["/site/a.html"] == "" {
		t.Error("--timestamps: the second run sent no If-Modified-Since")
	}

	spiderRoot := t.TempDir() + "/"
	if code := runGhola(t, "mirror", "-q", "--spider", "--robots=false", "-o", spiderRoot, ts.URL+"/site/"); code != 22 {
		t.Errorf("--spider with a broken link: exit %d, want 22", code)
	}
	if entries, _ := os.ReadDir(spiderRoot); len(entries) != 0 {
		t.Errorf("--spider saved %d files", len(entries))
	}
}
