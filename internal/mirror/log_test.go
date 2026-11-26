package mirror

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A page can hold a link of many megabytes. The log names it in a short
// form: a full copy in each log line fills the terminal and the log file.
func TestMirror_LogShortensAHugeLink(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<a href="/`+strings.Repeat("a", 100_000)+`">x</a>`) //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	var log strings.Builder
	m := &Mirror{Base: baseOptions(t, ts.URL+"/"), Root: t.TempDir(), Log: &log,
		Opts: config.MirrorOptions{On: true, Depth: 2, Layout: "no-host"}}
	m.Run(context.Background()) //nolint:errcheck // the long link fails
	for _, line := range strings.Split(log.String(), "\n") {
		if len(line) > maxLogLine+100 {
			t.Fatalf("a log line has %d bytes", len(line))
		}
	}
	if !strings.Contains(log.String(), "Failed") {
		t.Errorf("the failed link is not in the log: %.300s", log.String())
	}
}
