package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A folder that the command names for the remote file name is made:
// download -o DIR/, aria2c -d DIR and a dir= line of --input. Before the
// fix each one exited 23 when the folder did not exist.
func TestExecute_NamedFolderIsMade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("0123456789")) //nolint:errcheck
	}))
	defer srv.Close()
	base := t.TempDir()
	input := filepath.Join(base, "in.txt")
	if err := os.WriteFile(input, []byte(srv.URL+"/i.bin\n  dir="+filepath.Join(base, "d3")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		file string
	}{
		{[]string{"download", "-q", "-o", filepath.Join(base, "d1") + "/", srv.URL + "/a.bin"}, filepath.Join(base, "d1", "a.bin")},
		{[]string{"aria2c", "-q", "-d", filepath.Join(base, "d2"), srv.URL + "/b.bin"}, filepath.Join(base, "d2", "b.bin")},
		{[]string{"download", "-q", "-i", input}, filepath.Join(base, "d3", "i.bin")},
	} {
		if code := runGhola(t, c.args...); code != 0 {
			t.Errorf("%v: exit %d, want 0", c.args, code)
		}
		if _, err := os.Stat(c.file); err != nil {
			t.Errorf("%v: %v", c.args, err)
		}
	}
}
