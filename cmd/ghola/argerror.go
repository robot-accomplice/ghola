package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ANSI SGR codes for the argument-error line.
const (
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

// reportArgError writes an argument error to w: the usage text, then the
// error, then the usage text again. The error is red when color is true.
func reportArgError(w io.Writer, err error, color bool) {
	msg := err.Error()
	if color {
		msg = ansiRed + msg + ansiReset
	}
	var ue *config.UsageError
	if !errors.As(err, &ue) {
		fmt.Fprintln(w, msg)
		return
	}
	fmt.Fprintf(w, "%s\n%s\n\n%s", ue.Usage, msg, ue.Usage)
}

// useColor reports whether f can show ANSI colour: NO_COLOR (no-color.org)
// is unset or empty, TERM is not "dumb", and f is a terminal that accepts
// ANSI codes.
func useColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return enableANSI(f)
}
