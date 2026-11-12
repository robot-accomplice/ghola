package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	return <-done
}

// A user who types a bad command must see how to type it correctly before
// and after the error, and the error must stand out in red. The exact layout
// is the contract, so the test compares the full output.
func TestReportArgError_RedErrorBetweenUsage(t *testing.T) {
	ue := &config.UsageError{Err: errors.New("URL is mandatory"), Usage: "USAGE\n"}
	var buf bytes.Buffer
	reportArgError(&buf, ue, true)

	want := "USAGE\n\n" + ansiRed + "URL is mandatory" + ansiReset + "\n\nUSAGE\n"
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
}

// When colour is off (pipe, file, NO_COLOR), the output must contain no
// escape codes, because they corrupt logs and scripts that read stderr.
func TestReportArgError_NoColorHasNoEscapeCodes(t *testing.T) {
	ue := &config.UsageError{Err: errors.New("URL is mandatory"), Usage: "USAGE\n"}
	var buf bytes.Buffer
	reportArgError(&buf, ue, false)

	want := "USAGE\n\nURL is mandatory\n\nUSAGE\n"
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
}

// End to end: a missing URL prints the usage twice with the error between.
func TestExecute_ArgumentErrorPrintsUsageAroundError(t *testing.T) {
	var code int
	out := captureStderr(t, func() {
		code = execute(bg(), []string{}, nil, nil, os.Stdout)
	})
	if code != config.BadFlag.Int() {
		t.Errorf("exit code = %d, want %d (BadFlag)", code, config.BadFlag.Int())
	}
	const usageLine = "Usage: ghola [options...] <url>"
	if n := strings.Count(out, usageLine); n != 2 {
		t.Fatalf("usage printed %d times, want 2:\n%s", n, out)
	}
	first := strings.Index(out, usageLine)
	last := strings.LastIndex(out, usageLine)
	errAt := strings.Index(out, "URL is mandatory")
	if errAt < first || errAt > last {
		t.Errorf("error is not between the two usage blocks:\n%s", out)
	}
}

// Only argument errors get the usage text. A request that fails is not a
// usage problem: printing the usage there would hide the real cause.
func TestExecute_RequestFailureHasNoUsage(t *testing.T) {
	failing := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return errors.New("connection refused")
	}
	var code int
	out := captureStderr(t, func() {
		code = execute(bg(), []string{"http://test"}, failing, nil, os.Stdout)
	})
	if code != config.SendFailed.Int() {
		t.Errorf("exit code = %d, want %d (SendFailed)", code, config.SendFailed.Int())
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("stderr is missing the failure cause:\n%s", out)
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("request failure printed usage:\n%s", out)
	}
}

// Colour must be off when NO_COLOR is set or stderr is not a terminal.
func TestUseColor_OffForNoColorAndNonTerminal(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if useColor(f) {
		t.Error("useColor = true for a regular file, want false")
	}

	t.Setenv("NO_COLOR", "1")
	if useColor(os.Stderr) {
		t.Error("useColor = true with NO_COLOR set, want false")
	}
}
