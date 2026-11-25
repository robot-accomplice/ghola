package main

import (
	"net"
	"strings"
	"testing"
)

// -q hides notes and the progress, never the error (spec: "Errors still go
// to stderr"). ghola mirror -q wrote nothing on a failure before.
func TestExecute_MirrorQuietStillWritesTheError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + l.Addr().String() + "/"
	l.Close()
	var code int
	msg := captureStderr(t, func() { code = runGhola(t, "mirror", "-q", "-r", "0", "-o", t.TempDir()+"/", dead) })
	if code != 7 {
		t.Errorf("exit %d, want 7", code)
	}
	if !strings.Contains(msg, "Failed:") {
		t.Errorf("stderr %q, want the error", msg)
	}
}
