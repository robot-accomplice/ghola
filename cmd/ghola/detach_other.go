//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the process in a session of its own, so it outlives the
// terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
