package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// --jobs runs up to N transfers at the same time, as curl -Z and aria2c
// -j do; without it they run one after the other. Each body goes to its
// own file. The exit code is the last failure in URL order, whatever the
// timing. Several jobs on stdout would mix the bodies, so they are an
// argument error.
// peakServer answers each path with the path after 150 ms, and counts
// the most requests that it served at the same time in peak.
func peakServer(peak *atomic.Int32) *httptest.Server {
	var now atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := now.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			// another job raised the peak first: read it again
		}
		time.Sleep(150 * time.Millisecond)
		now.Add(-1)
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, r.URL.Path)
	}))
}

// checkJobFiles checks that each job wrote its own body to its own file.
func checkJobFiles(t *testing.T, jobs, dir string) {
	t.Helper()
	for _, f := range []string{"a", "b", "c", "d"} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); string(b) != "/"+f {
			t.Errorf("-j %s: %s = %q", jobs, f, b)
		}
	}
}

func TestExecute_Jobs(t *testing.T) {
	var peak atomic.Int32
	srv := peakServer(&peak)
	defer srv.Close()
	urls := []string{srv.URL + "/a", srv.URL + "/b", srv.URL + "/c", srv.URL + "/d"}
	for _, tc := range []struct {
		jobs      string
		wantPeak  int32
		wantFiles bool
	}{{"4", 4, true}, {"1", 1, true}} {
		peak.Store(0)
		dir := t.TempDir() + "/"
		if code := runGhola(t, append([]string{"download", "-q", "-j", tc.jobs, "-o", dir}, urls...)...); code != 0 {
			t.Fatalf("-j %s: exit %d", tc.jobs, code)
		}
		if p := peak.Load(); p != tc.wantPeak {
			t.Errorf("-j %s: %d transfers at the same time, want %d", tc.jobs, p, tc.wantPeak)
		}
		checkJobFiles(t, tc.jobs, dir)
	}
	dir := t.TempDir() + "/"
	if code := runGhola(t, "download", "-q", "-r", "0", "-j", "2", "-o", dir, srv.URL+"/missing", srv.URL+"/e"); code != 22 {
		t.Errorf("a failing URL: exit %d, want 22", code)
	}
	if code := runGhola(t, "-q", "-j", "2", srv.URL+"/a", srv.URL+"/b"); code != 2 {
		t.Errorf("-j 2 on stdout: exit %d, want 2", code)
	}
}
