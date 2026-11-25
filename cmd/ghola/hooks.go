package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/jobs"
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
	cmd := exec.Command(command, jobs.ID(opts.URL, opts.Output.File), "1", opts.Output.File) //nolint:gosec // the user's own command
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := quoteForBatch(cmd); err != nil {
		if !opts.Output.Silent {
			fmt.Fprintf(os.Stderr, "Warning: %s not run: %v\n", command, err)
		}
		return
	}
	if err := cmd.Run(); err != nil && !opts.Output.Silent {
		fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", command, err)
	}
}
