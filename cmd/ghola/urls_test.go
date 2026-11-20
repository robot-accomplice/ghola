package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// TestExecute_SeveralURLs checks that ghola fetches each URL in order and
// writes the bodies to stdout one after the other, as curl does (audit
// finding M9). A failure does not stop the run, and the exit code is the
// last failure's, as curl does (v1 defaults, item 10B): a script that
// fetches a list gets every file that exists.
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
	code, body := run(srv.URL+"/missing", srv.URL+"/two") // failing at 400 is the v1 default
	if code != 22 || hits.Load() != 2 || !strings.HasSuffix(body, "/two") {
		t.Errorf("first URL fails: exit %d, %d requests, stdout %q; want 22, 2 and /two at the end", code, hits.Load(), body)
	}
	if code, _ := run("-r", "0", srv.URL+"/missing", deadURL(t)); code != 7 {
		t.Errorf("two failures: exit %d, want 7, the last one", code)
	}
}

// Several -o pair with several URLs in order (v1 spec, output group), so
// a second URL cannot replace the file of the first. One -o DIR/ serves
// all URLs, and another count is an argument error.
func TestExecute_OutputForEachURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path) //nolint:errcheck
	}))
	defer srv.Close()
	dir := t.TempDir()
	a, b := dir+"/a", dir+"/b"
	if code := runGhola(t, "-q", "-o", a, "-o", b, srv.URL+"/one", srv.URL+"/two"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for f, want := range map[string]string{a: "/one", b: "/two"} {
		if got, _ := os.ReadFile(f); string(got) != want {
			t.Errorf("%s = %q, want %q", f, got, want)
		}
	}
	if code := runGhola(t, "-q", "-o", dir+"/", srv.URL+"/three", srv.URL+"/four"); code != 0 {
		t.Fatalf("-o DIR/: exit %d", code)
	}
	for _, f := range []string{"three", "four"} {
		if got, _ := os.ReadFile(dir + "/" + f); string(got) != "/"+f {
			t.Errorf("-o DIR/: %s = %q", f, got)
		}
	}
	if code := runGhola(t, "-q", "-o", a, srv.URL+"/one", srv.URL+"/two"); code != 2 {
		t.Errorf("one -o FILE for two URLs: exit %d, want 2", code)
	}
}
