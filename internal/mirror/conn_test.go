package mirror

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A mirror of many files reuses its connections. One new connection for
// each file runs out of file descriptors on a large site (ulimit -n 1024
// stopped a run after about 1,000 files), because the connections stay
// open until the process ends.
func TestMirror_ReusesConnectionsAcrossFiles(t *testing.T) {
	const pages = 40
	var opened atomic.Int64
	var links strings.Builder
	for i := range pages {
		fmt.Fprintf(&links, `<a href="p%d.html">p</a>`, i)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			io.WriteString(w, links.String()) //nolint:errcheck
			return
		}
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "<html>page</html>") //nolint:errcheck
	}))
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	ts.Start()
	t.Cleanup(ts.Close)

	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 2, Robots: true, Layout: "no-host"}}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := opened.Load(); n > 2 {
		t.Errorf("%d files opened %d connections; one shared transport reuses one", pages+2, n)
	}
}
