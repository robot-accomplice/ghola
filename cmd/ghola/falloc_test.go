package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// --file-allocation falloc needs Linux. Elsewhere it is an argument error
// (exit 2) before any request, not a write error after the probe.
func TestExecute_FallocOutsideLinuxIsAnArgumentError(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("falloc works on Linux")
	}
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Write([]byte("x")) //nolint:errcheck
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "f")
	if code := runGhola(t, "download", "-q", "-n", "2", "--file-allocation", "falloc", "-o", out, srv.URL); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests before the argument error, want 0", n)
	}
}
