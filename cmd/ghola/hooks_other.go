//go:build !windows

package main

import "os/exec"

// quoteForBatch is only needed on Windows, where cmd.exe runs batch files.
func quoteForBatch(*exec.Cmd) error { return nil }
