package main

import (
	"strings"
	"testing"
)

// The job word must be the first argument (spec section 1, rule 1). After
// an option it is a URL: ghola -v download URL fetched http://download/.
// ghola says so, so the user sees why the download did not run.
func TestExecute_JobWordAfterAnOptionIsNoted(t *testing.T) {
	msg := captureStderr(t, func() { runGhola(t, "-r", "0", "--timeout-connect", "1s", "download", "http://127.0.0.1:1/x") })
	if !strings.Contains(msg, `"download"`) || !strings.Contains(msg, "first argument") {
		t.Errorf("stderr %q, want a note about the job word", msg)
	}
}
