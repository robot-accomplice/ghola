package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// -q hides the meter and the notes, not the body or the errors (v1 spec:
// "Errors still go to stderr"), as curl -sS does. v0 -s wrote nothing at
// all, and a v1 user who types -q expects the body.
func TestExecute_QuietKeepsTheBodyAndTheErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("the body")) //nolint:errcheck
	}))
	defer srv.Close()
	out, _ := os.CreateTemp(t.TempDir(), "stdout")
	defer out.Close()
	if code := execute(context.Background(), []string{"-q", srv.URL}, nil, nil, out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if b, _ := os.ReadFile(out.Name()); string(b) != "the body" {
		t.Errorf("stdout = %q, want the body", b)
	}
	stderr := captureStderr(t, func() {
		execute(context.Background(), []string{"-q", "-r", "0", srv.URL + "/missing"}, nil, nil, out)
	})
	if !strings.Contains(stderr, "404") {
		t.Errorf("stderr = %q, want the error", stderr)
	}
}
