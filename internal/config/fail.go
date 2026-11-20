package config

import (
	"errors"
	"fmt"
	"io"
)

// ErrHTTPStatus is returned when --fail is set and the server answers with
// a status that --fail refuses. The stdout path and the download path both
// return it, so both give the same exit code.
var ErrHTTPStatus = errors.New("non-2xx http status")

// FailsOn reports whether --fail refuses status. This is ghola's v0 rule:
// any status outside 2xx. curl -f refuses only 400 and above; the v1 flag
// design decides which rule v1 keeps.
func (o OutputOptions) FailsOn(status int) bool {
	if o.FailFrom400 {
		return o.Fail && status >= 400 // the v1 rule (v1 defaults, item 4), as in curl
	}
	return o.Fail && (status < 200 || status >= 300)
}

// ErrWrite marks an error that happened while ghola wrote the output, to
// stdout or to a file. It exits 23, as curl's write error.
var ErrWrite = errors.New("write error")

// WriteError marks err as a write error. It returns nil for nil.
func WriteError(err error) error {
	if err == nil || errors.Is(err, ErrWrite) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrWrite, err)
}

// MarkWrites returns w with its errors marked as write errors. An io.Copy
// from a body to w can then tell a failed write from a failed read.
func MarkWrites(w io.Writer) io.Writer { return markedWriter{w} }

type markedWriter struct{ w io.Writer }

func (m markedWriter) Write(p []byte) (int, error) {
	n, err := m.w.Write(p)
	return n, WriteError(err)
}
