package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/robot-accomplice/ghola/internal/config"
)

// backgroundChild marks the process that --background started, so it
// does not start another one.
const backgroundChild = "GHOLA_BACKGROUND_CHILD"

// defaultLog is the --log file of --background, as wget -b writes
// wget-log.
const defaultLog = "ghola.log"

// startBackground starts ghola again with the same arguments, detached,
// and returns: the new process writes to --log (ghola.log by default),
// as wget -b does. done is false in the process that runs the job.
func startBackground(opts *config.Options, args []string, w *os.File) (code int, done bool) {
	if !opts.Output.Background || os.Getenv(backgroundChild) != "" {
		return 0, false
	}
	if opts.Output.Log == "" {
		opts.Output.Log = defaultLog
		args = append(args, "--log", defaultLog)
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "--background: %v\n", err)
		return config.SendFailed.Int(), true
	}
	log, err := os.OpenFile(opts.Output.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--log: %v\n", err)
		return config.WriteFileFailed.Int(), true
	}
	defer log.Close()
	cmd := exec.Command(self, args...) //nolint:gosec // ghola itself, with the user's arguments
	cmd.Env = append(os.Environ(), backgroundChild+"=1")
	cmd.Stdout, cmd.Stderr = log, log // everything goes to the log, as with wget -b
	detach(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "--background: %v\n", err)
		return config.SendFailed.Int(), true
	}
	fmt.Fprintf(w, "Continuing in background, pid %d. Output goes to %s.\n", cmd.Process.Pid, opts.Output.Log)
	return config.NoError.Int(), true
}

// openLog sends ghola's messages to --log, appended, as wget -a does. It
// returns the function that puts stderr back.
func openLog(opts *config.Options) (func(), error) {
	if opts.Output.Log == "" {
		return func() {}, nil
	}
	f, err := os.OpenFile(opts.Output.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("--log: %w", err)
	}
	stderr := os.Stderr
	os.Stderr = f
	return func() { os.Stderr = stderr; f.Close() }, nil
}
