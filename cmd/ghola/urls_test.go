package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// TestExecute_SeveralURLs checks that ghola fetches each URL in order and
// writes the bodies to stdout one after the other, as curl does (audit
// finding M9). It stops at the first failure and exits with its code.
func TestExecute_SeveralURLs(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, r.URL.Path) //nolint:errcheck
	}))
	defer srv.Close()

	run := func(args ...string) (int, string) {
		out, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		code := execute(context.Background(), args, nil, nil, out)
		b, _ := os.ReadFile(out.Name())
		return code, string(b)
	}

	if code, body := run(srv.URL+"/one", srv.URL+"/two"); code != 0 || body != "/one/two" {
		t.Errorf("two URLs: exit %d, stdout %q; want 0 and %q", code, body, "/one/two")
	}

	hits.Store(0)
	code, _ := run(srv.URL+"/missing", srv.URL+"/two") // failing at 400 is the v1 default
	if code != 22 || hits.Load() != 1 {
		t.Errorf("first URL fails: exit %d, %d requests; want 22 and 1", code, hits.Load())
	}
}
