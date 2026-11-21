package main

import (
	"os/exec"
	"syscall"
)

// detach starts the process without a console, apart from this one.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008 // DETACHED_PROCESS
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}
