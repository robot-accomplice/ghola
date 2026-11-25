package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// The hook gets the same job ID that "ghola jobs list" shows, and the
// absolute file path, as aria2c gives its hooks. Before, the hook hashed
// the relative path, so its ID matched no job.
func TestRunHook_SameJobIDAsTheJobList(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	record := filepath.Join(dir, "hook.out")
	t.Setenv("GHOLA_TEST_HOOK_OUT", record)
	opts := &config.Options{URL: "http://h/f.bin"}
	opts.Output.File = "f.bin" // relative, as -o f.bin gives it
	runHook(self, opts)
	got, err := os.ReadFile(record)
	parts := strings.Split(string(got), "|")
	abs := filepath.Join(dir, "f.bin")
	if err != nil || len(parts) != 3 {
		t.Fatalf("hook args %q, %v", got, err)
	}
	if want := jobID(opts); parts[0] != want {
		t.Errorf("hook job ID %s, want the job list ID %s", parts[0], want)
	}
	if parts[2] != abs {
		t.Errorf("hook file %q, want %q", parts[2], abs)
	}
}
