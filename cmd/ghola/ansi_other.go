//go:build !windows

package main

import "os"

// enableANSI reports whether f renders ANSI codes. Unix terminals do so
// without setup.
func enableANSI(*os.File) bool { return true }
