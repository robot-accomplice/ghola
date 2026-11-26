package mirror

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A second run with --timestamps gets 304 for a page that did not
// change. The mirror still reads the links of the kept file, also when
// its name has no .html extension and the 304 has no Content-Type, so a
// new page that it links to is fetched.
func TestMirror_TimestampsReadsTheLinksOfAnUnchangedPage(t *testing.T) {
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/deep.html" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>deep</html>") //nolint:errcheck
			return
		}
		if r.URL.Path != "/page.php" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified) // no Content-Type, as many servers do
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
		io.WriteString(w, `<html><a href="deep.html">d</a></html>`) //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	root := t.TempDir()
	run := func() []string {
		mu.Lock()
		paths = nil
		mu.Unlock()
		m := &Mirror{Base: baseOptions(t, ts.URL+"/page.php"), Root: root,
			Opts: config.MirrorOptions{On: true, Depth: 2, Timestamps: true, Layout: "no-host"}}
		if err := m.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
	run()
	if got := run(); !slices.Contains(got, "/deep.html") {
		t.Errorf("the second run did not read the links of the unchanged page: %v", got)
	}
}
