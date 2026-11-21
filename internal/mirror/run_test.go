package mirror

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/cli"
	"github.com/robot-accomplice/ghola/internal/config"
)

// site is a small web site: a page that links to a page, a style sheet
// with an image, an image, a nofollow link, a folder that robots.txt
// closes, and a link to another host.
func site(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	pages := map[string][2]string{
		"/robots.txt":     {"text/plain", "User-agent: *\nDisallow: /private/\n"},
		"/":               {"text/html", `<html><head><link rel="stylesheet" href="/style.css"></head><body><a href="a.html">a</a> <img src="img.png"> <a rel="nofollow" href="/skip.html">x</a> <a href="/private/p.html">p</a> <a href="http://other.example/">o</a></body></html>`},
		"/a.html":         {"text/html", `<html><body><a href="/">home</a><a href="/missing.html">m</a></body></html>`},
		"/style.css":      {"text/css", `body { background: url("bg.png") }`},
		"/bg.png":         {"image/png", "png-bg"},
		"/img.png":        {"image/png", "png-img"},
		"/skip.html":      {"text/html", "<html>skip</html>"},
		"/private/p.html": {"text/html", "<html>private</html>"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		p, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", p[0])
		io.WriteString(w, p[1]) //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func baseOptions(t *testing.T, url string) *config.Options {
	t.Helper()
	c, err := cli.Parse([]string{cli.JobDownload, "-q", "-r", "0", url})
	if err != nil {
		t.Fatal(err)
	}
	o, err := cli.Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// A mirror saves the pages and what they need, follows only links that
// the scope and robots.txt allow, and rewrites the saved links to the
// local files, as wget -m -k does. A missing page fails the run but does
// not stop it.
func TestMirror_Run(t *testing.T) {
	ts, seen := site(t)
	root := t.TempDir()
	var log strings.Builder
	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: root, Log: &log,
		Opts: config.MirrorOptions{On: true, Depth: 3, Robots: true, Requisites: true, Convert: true, Layout: "no-host"}}
	err := m.Run(context.Background())
	if err == nil {
		t.Error("a missing page must make the run fail")
	}
	if m.exitCode(err) != config.SendFailed.Int() {
		t.Errorf("without ExitCode the code is %d", m.exitCode(err))
	}
	for _, f := range []string{"index.html", "a.html", "style.css", "bg.png", "img.png"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s was not saved: %v", f, err)
		}
	}
	got := strings.Join(seen(), " ")
	for _, never := range []string{"/skip.html", "/private/p.html"} {
		if strings.Contains(got, never) {
			t.Errorf("%s was fetched: nofollow and robots.txt close it (%s)", never, got)
		}
	}
	index, _ := os.ReadFile(filepath.Join(root, "index.html"))
	// A saved file gets a local link; a file that was not saved gets the
	// absolute URL, so the copy still reaches it (wget -k).
	if !strings.Contains(string(index), `href="a.html"`) || !strings.Contains(string(index), `href="`+ts.URL+`/skip.html"`) {
		t.Errorf("links not converted: %s", index)
	}
	if !strings.Contains(log.String(), "Saved") || !strings.Contains(log.String(), "missing.html") {
		t.Errorf("log: %s", log.String())
	}
}

// --spider checks every link and saves nothing; a broken link fails it.
func TestMirror_Spider(t *testing.T) {
	ts, _ := site(t)
	root := t.TempDir()
	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: root, ExitCode: func(error) int { return 22 },
		Opts: config.MirrorOptions{On: true, Depth: 2, Spider: true}}
	if err := m.Run(context.Background()); err == nil {
		t.Error("the broken link /missing.html passed")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("--spider saved %d files", len(entries))
	}
}

// --quota stops the run after the bytes, and a stopped context ends it.
func TestMirror_QuotaAndCancel(t *testing.T) {
	ts, seen := site(t)
	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 3, Quota: 1}}
	m.Run(context.Background()) //nolint:errcheck
	if n := len(seen()); n != 1 {
		t.Errorf("--quota 1: %d requests, want 1", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m2 := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(), Opts: config.MirrorOptions{On: true}}
	m2.Run(ctx) //nolint:errcheck
}

func TestPageKind(t *testing.T) {
	for _, c := range [][3]string{
		{"text/html; charset=utf-8", "x", "html"}, {"text/css", "x", "css"}, {"", "a.htm", "html"},
		{"", "s.css", "css"}, {"image/png", "i.png", ""},
	} {
		if got := pageKind(c[0], c[1]); got != c[2] {
			t.Errorf("%q %q: %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
