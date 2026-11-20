package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --cond-time FILE sends If-Modified-Since the time of FILE. A 304 then
// writes no file and exits 0, as curl -z does: "-z f -o f" refreshes f
// only when the server has a newer one, and keeps f otherwise. A FILE that
// does not exist yet sends no condition, so the first run gets the file.
func TestExecute_CondTime(t *testing.T) {
	remote := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified) // ServeContent checks GET and HEAD only
			return
		}
		http.ServeContent(w, r, "f", remote, strings.NewReader("new"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	newer, older := filepath.Join(dir, "newer"), filepath.Join(dir, "older")
	for name, mtime := range map[string]time.Time{newer: remote.Add(time.Hour), older: remote.Add(-time.Hour)} {
		if err := os.WriteFile(name, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, file, want string
		args             []string
	}{
		{"fetch, not modified: no file", filepath.Join(dir, "a"), "", []string{"--cond-time", newer}},
		{"fetch, modified", filepath.Join(dir, "b"), "new", []string{"--cond-time", older}},
		{"POST, not modified: no file", filepath.Join(dir, "p"), "", []string{"-d", "x", "--cond-time", newer}},
		{"fetch, no file yet: no condition", filepath.Join(dir, "c"), "new", []string{"--cond-time", filepath.Join(dir, "missing")}},
		{"download, not modified: the file stays", newer, "old", []string{"download", "--cond-time", newer}},
		{"download, modified", older, "new", []string{"download", "--output-overwrite", "--cond-time", older}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append(tc.args, "-q", "-o", tc.file, srv.URL)
			out, _ := os.CreateTemp(t.TempDir(), "out")
			defer out.Close()
			if code := execute(context.Background(), args, nil, nil, out); code != 0 {
				t.Fatalf("exit %d", code)
			}
			b, err := os.ReadFile(tc.file)
			if tc.want == "" {
				if err == nil {
					t.Errorf("a 304 wrote the file: %q", b)
				}
				return
			}
			if string(b) != tc.want {
				t.Errorf("file = %q, want %q (err %v)", b, tc.want, err)
			}
		})
	}
}
