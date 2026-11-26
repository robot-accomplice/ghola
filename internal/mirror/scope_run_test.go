package mirror

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// recorder is a server that records the paths it serves. Each page is
// HTML; a path that starts with "->" in pages is a redirect to the rest.
func recorder(t *testing.T, pages map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body, ok := pages[r.URL.Path]
		switch {
		case !ok:
			http.NotFound(w, r)
		case strings.HasPrefix(body, "->"):
			http.Redirect(w, r, strings.TrimPrefix(body, "->"), http.StatusFound)
		case strings.HasPrefix(body, "status:"):
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, body) //nolint:errcheck
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// A redirect is a link: a redirect to a host that --hosts does not name
// is not followed, so the mirror does not fetch or save a file of
// another site. With the host in --hosts, it is followed.
func TestMirror_RedirectFollowsTheHostRule(t *testing.T) {
	other, otherSeen := recorder(t, map[string]string{"/x.html": "<html>other</html>"})
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1) // another host name, same machine
	start, _ := recorder(t, map[string]string{
		"/":  `<a href="/r">r</a>`,
		"/r": "->" + otherURL + "/x.html",
	})
	root := t.TempDir()
	var log strings.Builder
	m := &Mirror{Base: baseOptions(t, start.URL+"/"), Root: root, Log: &log,
		Opts: config.MirrorOptions{On: true, Depth: 2, Layout: "no-host"}}
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("a redirect out of scope is skipped, not a failure: %v", err)
	}
	if got := otherSeen(); len(got) != 0 {
		t.Errorf("the other host was fetched: %v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "r")); err == nil {
		t.Error("the answer of the other host was saved as r")
	}
	if !strings.Contains(log.String(), otherURL) {
		t.Errorf("the log does not say why /r was skipped: %s", log.String())
	}

	m = &Mirror{Base: baseOptions(t, start.URL+"/"), Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 2, Layout: "no-host", Hosts: []string{"127.0.0.1", "localhost"}}}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := otherSeen(); !slices.Contains(got, "/x.html") {
		t.Errorf("--hosts names the other host, but it was not fetched: %v", got)
	}
}

// The start URL is what the user asked for: a scope rule that does not
// match it does not drop it, as in wget. The rules apply to its links.
func TestMirror_ScopeRulesKeepTheStartURL(t *testing.T) {
	for name, opts := range map[string]config.MirrorOptions{
		"--include-dirs": {IncludeDirs: []string{"/docs"}},
		"--accept-regex": {AcceptRegex: regexp.MustCompile(`/docs/`)},
		"--hosts":        {Hosts: []string{"example.invalid"}},
	} {
		t.Run(name, func(t *testing.T) {
			ts, seen := recorder(t, map[string]string{
				"/":            `<a href="/docs/d.html">d</a> <a href="/other.html">o</a>`,
				"/docs/d.html": "<html>d</html>",
				"/other.html":  "<html>o</html>",
			})
			opts.On, opts.Depth, opts.Layout = true, 2, "no-host"
			m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(), Opts: opts}
			if err := m.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := seen()
			if !slices.Contains(got, "/") {
				t.Errorf("the start URL was not fetched: %v", got)
			}
			if slices.Contains(got, "/other.html") {
				t.Errorf("a link out of scope was fetched: %v", got)
			}
		})
	}
}

// A robots.txt that answers 5xx closes the whole site (RFC 9309, 2.3.1.4):
// the server may be down or overloaded, and the crawler must not take
// that as permission. A 4xx answer still opens the whole site.
func TestMirror_RobotsServerErrorDisallowsAll(t *testing.T) {
	ts, seen := recorder(t, map[string]string{
		"/robots.txt": "status:503",
		"/":           `<a href="a.html">a</a>`,
		"/a.html":     "<html>a</html>",
	})
	var log strings.Builder
	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(), Log: &log,
		Opts: config.MirrorOptions{On: true, Depth: 2, Robots: true, Layout: "no-host"}}
	m.Run(context.Background()) //nolint:errcheck
	if got := seen(); !slices.Equal(got, []string{"/robots.txt"}) {
		t.Errorf("robots.txt answered 503, but the mirror fetched %v", got)
	}
	if !strings.Contains(log.String(), "robots.txt") {
		t.Errorf("the log does not say why nothing was fetched: %s", log.String())
	}

	ts4, seen4 := recorder(t, map[string]string{"/": "<html>x</html>"})
	m = &Mirror{Base: baseOptions(t, ts4.URL+"/"), Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 2, Robots: true, Layout: "no-host"}}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := seen4(); !slices.Contains(got, "/") {
		t.Errorf("robots.txt answered 404, so all is allowed, but the mirror fetched %v", got)
	}
}
