package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --on-complete runs after a download that worked, and --on-error after
// one that failed, with aria2's arguments: the job ID, the number of
// files and the file path. The hook does not change the exit code.
func TestExecute_Hooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, hook string
		want             int
	}{{"complete", "/f", "--on-complete", 0}, {"error", "/missing", "--on-error", 22}} {
		record := filepath.Join(dir, tc.name+".hook")
		t.Setenv("GHOLA_TEST_HOOK_OUT", record)
		file := filepath.Join(dir, tc.name)
		other := map[string]string{"--on-complete": "--on-error", "--on-error": "--on-complete"}[tc.hook]
		if code := runGhola(t, "download", "-q", "-r", "0", tc.hook, self, other, "does-not-exist", "-o", file, srv.URL+tc.path); code != tc.want {
			t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
		}
		got, err := os.ReadFile(record)
		parts := strings.Split(string(got), "|")
		if err != nil || len(parts) != 3 || len(parts[0]) != 16 || parts[1] != "1" || parts[2] != file {
			t.Errorf("%s: hook args %q, %v", tc.name, got, err)
		}
	}
}
