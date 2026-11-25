package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Two transfers must not write one file at the same time: the second one
// stops with a write error (exit 23), and the first one keeps the file.
// Before, "ghola jobs resume" next to a running download gave a file
// larger than the remote one.
func TestOpenOutput_SecondWriterIsRefused(t *testing.T) {
	name := filepath.Join(t.TempDir(), "f.bin")
	first, err := OpenOutput(OutputOptions{}, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenOutput(OutputOptions{}, name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, true)
	if err == nil {
		second.Close()
		t.Fatal("a second writer opened the file")
	}
	if !errors.Is(err, ErrWrite) || !errors.Is(err, ErrOutputInUse) {
		t.Errorf("error %v, want ErrWrite and ErrOutputInUse", err)
	}
	if InUse(name) != true {
		t.Error("InUse is false while the first writer has the file")
	}
	first.Close()
	if InUse(name) {
		t.Error("InUse is true after the writer closed the file")
	}
	again, err := OpenOutput(OutputOptions{}, name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, true)
	if err != nil {
		t.Fatalf("after the first writer closed: %v", err)
	}
	again.Close()
}
