package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// --upload - sends stdin as the body, as curl -T - does. Before, ghola
// tried to open a file named "-" (exit 26).
func TestReadBodySources_UploadFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("stdin upload\n") //nolint:errcheck
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
	opts := &config.Options{Transfer: "-"}
	if err := readBodySources(opts); err != nil || opts.Data != "stdin upload\n" {
		t.Fatalf("data %q, err %v", opts.Data, err)
	}
}

// An upload file is the body as it is: a file that starts with @ is not
// a file name. Before, its first line named a second file to read.
func TestReadBodySources_UploadStartingWithAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(path, []byte("@not-a-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &config.Options{Transfer: path}
	if err := readBodySources(opts); err != nil || opts.Data != "@not-a-file" {
		t.Fatalf("data %q, err %v", opts.Data, err)
	}
}
