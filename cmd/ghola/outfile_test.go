package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// The output file rules: --max-size stops a large body with curl's 63
// before ghola writes it, whether the server sends the length or not;
// ghola download keeps an existing file unless --output-overwrite, so a
// second run cannot destroy the first file; --output-create-dirs makes
// the folders of -o.
func TestExecute_OutputFileRules(t *testing.T) {
	body := strings.Repeat("x", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			w.(http.Flusher).Flush() // no Content-Length
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want config.ExitCode
	}{
		{"max-size, length", []string{"--max-size", "100", srv.URL}, 63},
		{"max-size, no length", []string{"--max-size", "100", srv.URL + "/chunked"}, 63},
		{"max-size, -o", []string{"--max-size", "100", "-o", filepath.Join(dir, "a"), srv.URL}, 63},
		{"max-size, download", []string{"download", "--max-size", "100", "-o", filepath.Join(dir, "b"), srv.URL + "/chunked"}, 63},
		{"max-size, large enough", []string{"--max-size", "1000", srv.URL + "/chunked"}, 0},
		{"download keeps a file", []string{"download", "-o", existing, srv.URL}, 23},
		{"fetch replaces a file", []string{"-o", filepath.Join(dir, "c"), srv.URL}, 0},
		{"create dirs", []string{"--output-create-dirs", "-o", filepath.Join(dir, "x", "y", "f"), srv.URL}, 0},
		{"no create dirs", []string{"-o", filepath.Join(dir, "z", "f"), srv.URL}, 23},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := quiet(tc.args)
			if got := runGhola(t, args...); got != tc.want.Int() {
				t.Errorf("ghola %q exit = %d, want %d", args, got, tc.want)
			}
		})
	}
	checkOutputFiles(t, dir, existing)
	// --output-overwrite replaces it.
	if got := runGhola(t, "download", "-q", "--output-overwrite", "-o", existing, srv.URL); got != 0 {
		t.Fatalf("--output-overwrite: exit %d", got)
	}
	if b, _ := os.ReadFile(existing); string(b) != body {
		t.Errorf("--output-overwrite kept the old file")
	}
}

// checkOutputFiles checks that download kept the existing file, and that
// no file grew past --max-size.
func checkOutputFiles(t *testing.T, dir, existing string) {
	t.Helper()
	if b, _ := os.ReadFile(existing); string(b) != "keep" {
		t.Errorf("download replaced the existing file: %q", b)
	}
	for _, name := range []string{"a", "b"} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Size() > 100 {
			t.Errorf("%s: %d bytes written past --max-size", name, fi.Size())
		}
	}
}

// quiet puts -q -r 0 after the job word, if there is one.
func quiet(args []string) []string {
	if len(args) > 0 && args[0] == "download" {
		return append([]string{"download", "-q", "-r", "0"}, args[1:]...)
	}
	return append([]string{"-q", "-r", "0"}, args...)
}

// runGhola runs ghola with args, with stdout in a temporary file, and
// returns the exit code.
func runGhola(t *testing.T, args ...string) int {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return execute(ctx, args, nil, nil, out)
}
