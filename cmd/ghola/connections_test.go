package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// -n is for segmented downloads. On stdout it raced n copies of the same
// request, so -n 3 -d once sent the POST three times: a side effect the
// user did not ask for (audit finding H4).
func TestExecute_ConnectionsDoesNotRepeatAPOST(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
	}))
	defer srv.Close()

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	// In v1, -n belongs to ghola download (spec scope), so a fetch with -n
	// is an argument error before any request: the POST is never repeated.
	if code := execute(context.Background(), []string{"-n", "3", "-d", "once", srv.URL}, nil, nil, out); code != config.BadFlag.Int() {
		t.Fatalf("exit %d, want %d (argument error)", code, config.BadFlag.Int())
	}
	if n := posts.Load(); n != 0 {
		t.Errorf("server got %d POSTs, want 0", n)
	}
}

// A failed request is an error exit, with -n as without it. Before, -n 3
// against a refused port printed nothing and exited 0 (audit finding H4).
func TestExecute_ConnectionsReportsAFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String() + "/"
	ln.Close()

	out, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if execute(context.Background(), []string{"-n", "3", dead}, nil, nil, out) == 0 {
		t.Error("exit 0 for a refused connection")
	}
}

// -f gives the same exit code with -o as on stdout (HTTPError, curl 22), and writes
// no file (audit finding H7).
func TestExecute_FailToFileExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()

	file := t.TempDir() + "/f"
	if code := execute(context.Background(), []string{"-q", "-o", file, srv.URL}, nil, nil, nil); code != config.HTTPError.Int() {
		t.Errorf("exit %d, want %d", code, config.HTTPError.Int())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the file exists (stat err %v)", err)
	}
}
