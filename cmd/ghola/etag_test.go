package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --cond-etag-save and --cond-etag-compare on one file make a cache, as
// curl --etag-save and --etag-compare do: the first run gets the file and
// saves the ETag; the next run sends If-None-Match, and a 304 writes no
// file and keeps the saved ETag. On stdout, fetch and download alike.
func TestExecute_ETagCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader("body"))
	}))
	defer srv.Close()
	for _, job := range []string{"stdout", "fetch", "download"} {
		t.Run(job, func(t *testing.T) {
			dir := t.TempDir()
			etag := filepath.Join(dir, "etag")
			if code := runGhola(t, etagArgs(job, etag, dir, "first", srv.URL)...); code != 0 {
				t.Fatalf("first run: exit %d", code)
			}
			if b, _ := os.ReadFile(etag); string(b) != "\"v1\"\n" {
				t.Fatalf("saved ETag %q", b)
			}
			if code := runGhola(t, etagArgs(job, etag, dir, "second", srv.URL)...); code != 0 {
				t.Fatalf("second run: exit %d", code)
			}
			if _, err := os.Stat(filepath.Join(dir, "second")); job != "stdout" && err == nil {
				t.Error("the 304 wrote the file")
			}
			if b, _ := os.ReadFile(etag); string(b) != "\"v1\"\n" {
				t.Errorf("after the 304: saved ETag %q", b)
			}
		})
	}
}

// etagArgs is one run of the ETag cache for the job, with -o dir/body.
func etagArgs(job, etag, dir, body, url string) []string {
	args := []string{"-q", "--cond-etag-compare", etag, "--cond-etag-save", etag}
	if job != "stdout" {
		args = append(args, "-o", filepath.Join(dir, body))
	}
	if job == "download" {
		args = append([]string{job}, args...)
	}
	return append(args, url)
}
