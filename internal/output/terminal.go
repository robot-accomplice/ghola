package output

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/robot-accomplice/ghola/internal/config"
	"golang.org/x/term"
)

// errBinaryTerminal refuses binary data to a terminal, as curl does (v1
// defaults, item 5). Binary bytes can change the terminal's state. The
// error is a write error, so it exits 23, as in curl.
var errBinaryTerminal = errors.New("binary output can mess up your terminal; use -o - to write it anyway, or -o FILE")

// isTerminal reports whether w is a terminal. Tests replace it.
var isTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// binaryGuard refuses a write that holds a NUL byte, as curl does for each
// write to a terminal. Text never holds NUL, and most binary formats do.
type binaryGuard struct{ w io.Writer }

func (g binaryGuard) Write(p []byte) (int, error) {
	if bytes.IndexByte(p, 0) >= 0 {
		return 0, config.WriteError(errBinaryTerminal)
	}
	return g.w.Write(p)
}

// bodyOut returns where the body goes on stdout: w itself, or w behind the
// binary check when w is a terminal and -o - was not given.
func bodyOut(w io.Writer, opts *config.Options) io.Writer {
	if opts.Output.Stdout || !isTerminal(w) {
		return w
	}
	return binaryGuard{w}
}
