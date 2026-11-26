package config

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// openLocked opens name and takes an exclusive lock on all of it, or fails
// at once with ErrOutputInUse when another handle holds one. Windows frees
// the lock when the file closes. LockFileEx needs write access, which an
// O_APPEND file does not have on Windows, so append opens the file for
// writing and starts at its end: only this handle writes, so that is the
// same.
func openLocked(name string, flags int) (*os.File, error) {
	appendMode := flags&os.O_APPEND != 0
	f, err := os.OpenFile(name, flags&^os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	if appendMode {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func lockFile(f *os.File) error {
	var ol windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrOutputInUse
	}
	return err
}

// inUse reports whether another handle holds the lock on name.
func inUse(name string) bool {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	return errors.Is(lockFile(f), ErrOutputInUse)
}
