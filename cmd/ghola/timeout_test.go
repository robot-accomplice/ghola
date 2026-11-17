package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stallServer sends headers and part of the body, then sends nothing more
// until the client goes away.
func stallServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		if r.Method == http.MethodHead {
			return
		}
		io.WriteString(w, "part") //nolint:errcheck
		w.(http.Flusher).Flush()
		// Stop after 10 s at most, so that srv.Close does not wait forever
		// when the client never gives up.
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runWithin runs execute and fails the test when it takes longer than limit.
func runWithin(t *testing.T, limit time.Duration, args ...string) int {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	done := make(chan int, 1)
	go func() { done <- execute(context.Background(), args, nil, nil, out) }()
	select {
	case code := <-done:
		return code
	case <-time.After(limit):
		t.Fatalf("ghola %v ran longer than %v: --timeout did not stop it", args, limit)
		return 0
	}
}

// --timeout covers the whole transfer, the body included, as curl's
// --max-time does. The stdout body is now read after FetchURL returns, so the
// deadline must outlive FetchURL.
func TestExecute_TimeoutStopsAStalledStdoutBody(t *testing.T) {
	srv := stallServer(t)
	// No -s: ghola's -s skips the body, so the stall would never be read.
	if runWithin(t, 5*time.Second, "--timeout", "300", srv.URL) == 0 {
		t.Error("exit status 0 for a body that did not arrive")
	}
}

// The same deadline applies to a download to a file (audit finding H3).
func TestExecute_TimeoutStopsAStalledFileDownload(t *testing.T) {
	srv := stallServer(t)
	file := filepath.Join(t.TempDir(), "f")
	if runWithin(t, 5*time.Second, "-s", "--timeout", "300", "-o", file, srv.URL) == 0 {
		t.Error("exit status 0 for a body that did not arrive")
	}
}

// A body that is slow but ends inside --timeout must arrive complete. The
// deadline must not end when FetchURL returns: the stdout body is read after
// that (audit finding M2).
func TestExecute_TimeoutAllowsASlowBodyThatEndsInTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "9")
		io.WriteString(w, "part") //nolint:errcheck
		w.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, "-rest") //nolint:errcheck
	}))
	defer srv.Close()

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if code := execute(context.Background(), []string{"--timeout", "5000", srv.URL}, nil, nil, out); code != 0 {
		t.Fatalf("exit status %d, want 0", code)
	}
	got, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "part-rest" {
		t.Errorf("stdout = %q, want \"part-rest\"", got)
	}
}
