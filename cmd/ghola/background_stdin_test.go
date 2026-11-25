package main

import (
	"os"
	"strings"
	"testing"
)

// --background starts a new process, and that process has no stdin: this
// one already read it. So --background with --input - is an argument
// error that names the fix. Before, ghola said "Continuing in background"
// and the background process failed with "--input - holds no URL".
func TestExecute_BackgroundRefusesInputFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("http://127.0.0.1:1/x\n") //nolint:errcheck
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	var code int
	msg := captureStderr(t, func() {
		code = runGhola(t, "download", "-q", "--background", "--log", t.TempDir()+"/l.log", "-i", "-")
	})
	if code != 2 || !strings.Contains(msg, "--input") {
		t.Errorf("exit %d, stderr %q; want exit 2 and a message about --input", code, msg)
	}
}
