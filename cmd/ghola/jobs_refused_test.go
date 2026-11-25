package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A download that is refused because the file exists (exit 23) must not
// enter the job list: the file is the user's, not a partial download.
// Before, "ghola jobs cancel" then deleted it and "ghola jobs resume"
// appended to it.
func TestExecute_RefusedDownloadIsNoJob(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("server bytes")) //nolint:errcheck
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(file, []byte("my own file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runGhola(t, "download", "-q", "-r", "0", "-o", file, srv.URL+"/report.docx"); code != 23 {
		t.Fatalf("exit %d, want 23", code)
	}
	if _, list := gholaStdout(t, "jobs", "list"); strings.Contains(list, file) {
		t.Fatalf("the refused download is in the job list:\n%s", list)
	}
	runGhola(t, "jobs", "resume")
	if b, err := os.ReadFile(file); err != nil || string(b) != "my own file\n" {
		t.Errorf("the user's file changed: %q, %v", b, err)
	}
}
