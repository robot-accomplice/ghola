package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrTooLarge marks a body larger than --max-size. curl exits 63 for it.
var ErrTooLarge = errors.New("the body is larger than --max-size")

// ErrOutputExists marks an output file that exists and that ghola did not
// write: --output-overwrite is off. The file is the user's, so the
// download is no job to resume or cancel.
var ErrOutputExists = errors.New("the output file exists")

// ErrOutputInUse marks an output file that another transfer writes now: a
// second ghola, or "ghola jobs resume" next to a running download. Two
// writers on one file would mix their bytes.
var ErrOutputInUse = errors.New("another transfer writes the output file")

// OpenOutput opens an output file with the rules of --output-create-dirs
// and --output-overwrite. Every file that ghola writes for a transfer
// opens here. ownFile is true when the file is ghola's own partial file (a
// resume, or a restart after the remote file changed): that may be
// replaced whatever --output-overwrite says.
func OpenOutput(o OutputOptions, name string, flags int, ownFile bool) (*os.File, error) {
	if o.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return nil, WriteError(err)
		}
	} else if o.Dir != "" && filepath.Dir(name) == filepath.Clean(o.Dir) {
		// -o DIR/ names the folder: make it, but not a missing parent.
		if err := os.Mkdir(o.Dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, WriteError(err)
		}
	}
	if o.NoOverwrite && !ownFile && flags&os.O_APPEND == 0 {
		flags |= os.O_EXCL
	}
	if flags&os.O_TRUNC != 0 && InUse(name) {
		return nil, fmt.Errorf("%w: %w: %s", ErrWrite, ErrOutputInUse, name) // O_TRUNC would empty the other writer's file
	}
	f, err := openLocked(name, flags)
	if errors.Is(err, os.ErrExist) {
		hint := o.ExistsHint
		if hint == "" {
			hint = "--output-overwrite replaces it"
		}
		return nil, fmt.Errorf("%w: %w: %s; %s", ErrWrite, ErrOutputExists, name, hint)
	}
	if errors.Is(err, ErrOutputInUse) {
		return nil, fmt.Errorf("%w: %w: %s", ErrWrite, err, name)
	}
	return f, WriteError(err)
}

// InUse reports whether a transfer writes name now: it holds the lock that
// OpenOutput takes. A file that does not exist is not in use.
func InUse(name string) bool { return inUse(name) }

// SaveETag writes the ETag of a 2xx answer to --cond-etag-save, as curl
// --etag-save does: the value as the server sent it, and a newline. With
// no ETag the file is empty. Another status leaves the file as it is, so
// a 304 to --cond-etag-compare keeps the saved ETag.
func (o OutputOptions) SaveETag(status int, etag string) error {
	if o.ETagSave == "" || status < 200 || status > 299 {
		return nil
	}
	if etag != "" {
		etag += "\n"
	}
	if err := os.WriteFile(o.ETagSave, []byte(etag), 0o644); err != nil {
		return fmt.Errorf("--cond-etag-save: %w", WriteError(err))
	}
	return nil
}

// SetTarget sets the output for -o VALUE: "-" is stdout; a value that
// ends in / or names a folder gets the remote file name inside it; else
// it is the file.
func (o *OutputOptions) SetTarget(v string) {
	o.Stdout, o.RemoteName, o.Dir, o.File = false, false, "", ""
	switch info, err := os.Stat(v); {
	case v == "-":
		o.Stdout = true
	case strings.HasSuffix(v, "/") || strings.HasSuffix(v, string(filepath.Separator)) || (err == nil && info.IsDir()):
		o.RemoteName, o.Dir = true, v
	default:
		o.File = v
	}
}

// TooLarge reports a declared length above --max-size. -1 is unknown.
func (o OutputOptions) TooLarge(length int64) bool {
	return o.MaxSize > 0 && length > o.MaxSize
}

// LimitSize returns w with --max-size applied: a write past the size
// fails with ErrTooLarge. already counts the bytes before (a resume).
func (o OutputOptions) LimitSize(w io.Writer, already int64) io.Writer {
	if o.MaxSize <= 0 {
		return w
	}
	return &sizeGuard{w: w, left: o.MaxSize - already}
}

type sizeGuard struct {
	w    io.Writer
	left int64
}

func (g *sizeGuard) Write(p []byte) (int, error) {
	if int64(len(p)) > g.left {
		return 0, ErrTooLarge
	}
	g.left -= int64(len(p))
	return g.w.Write(p)
}

// LimitReader is LimitSize for a body that ghola reads (stdout).
func (o OutputOptions) LimitReader(r io.Reader) io.Reader {
	if o.MaxSize <= 0 {
		return r
	}
	return &sizeReader{r: r, left: o.MaxSize}
}

type sizeReader struct {
	r    io.Reader
	left int64
}

func (s *sizeReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.left -= int64(n)
	if s.left < 0 {
		return 0, ErrTooLarge
	}
	return n, err
}
