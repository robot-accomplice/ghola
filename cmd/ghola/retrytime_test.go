package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// --retry-max-time stops the retries once the time has passed, on fetch
// and on download, as curl does: a script can bound the time of a job
// with many retries. Without it, every retry runs.
func TestExecute_RetryMaxTime(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	const retries = 8
	for _, tc := range []struct {
		name    string
		args    []string
		limited bool
	}{
		{"fetch", []string{"-q", "-r", "8", "--retry-delay", "0.1", "--retry-max-time", "0.25", srv.URL}, true},
		{"download", []string{"download", "-q", "-r", "8", "--retry-delay", "0.1", "--retry-max-time", "0.25", "-o", filepath.Join(t.TempDir(), "f"), srv.URL}, true},
		{"fetch, no limit", []string{"-q", "-r", "8", "--retry-delay", "0.01", srv.URL}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n.Store(0)
			out, _ := os.CreateTemp(t.TempDir(), "out")
			defer out.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			execute(ctx, tc.args, nil, nil, out)
			got := int(n.Load())
			if tc.limited && (got < 2 || got > 4) {
				t.Errorf("%d requests; want 2 to 4 in 0.25 s with a 0.1 s delay", got)
			}
			if !tc.limited && got != retries+1 {
				t.Errorf("%d requests; want %d", got, retries+1)
			}
		})
	}
}
