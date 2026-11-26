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
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A mirror of many files keeps few connections open. A connection that
// stays open for each file runs out of file descriptors on a large site
// (ulimit -n 1024 stopped a run after about 1,000 files).
func TestMirror_ReusesConnectionsAcrossFiles(t *testing.T) {
	const pages = 40
	var open atomic.Int64 // connections that the server has open now
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
		switch s {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	ts.Start()
	t.Cleanup(ts.Close)

	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 2, Robots: true, Layout: "no-host"}}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	const limit = 2 // net/http keeps at most 2 idle connections for each host
	for deadline := time.Now().Add(2 * time.Second); open.Load() > limit && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := open.Load(); n > limit {
		t.Errorf("%d files keep %d connections open, want at most %d", pages+2, n, limit)
	}
}
