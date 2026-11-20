package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"

	"github.com/robot-accomplice/ghola/internal/config"
)

// runHook runs --on-complete or --on-error after a download, as aria2c
// --on-download-complete does: the command gets the job ID, the number of
// files (1) and the file path, so aria2 hook scripts work as they are.
// ghola runs it without a shell and waits for it. Its result does not
// change the exit code of the download, as in aria2; a command that
// cannot start is a warning.
func runHook(command string, opts *config.Options) {
	if command == "" {
		return
	}
	cmd := exec.Command(command, jobID(opts.URL, opts.Output.File), "1", opts.Output.File) //nolint:gosec // the user's own command
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil && !opts.Output.Silent {
		fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", command, err)
	}
}

// jobID is a stable 16-digit hex ID of a download, as aria2's GID: the
// same URL and file give the same ID.
func jobID(url, file string) string {
	sum := sha256.Sum256([]byte(url + "\x00" + file))
	return hex.EncodeToString(sum[:8])
}
