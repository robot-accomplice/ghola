//go:build unix

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openLocked opens name and takes an exclusive lock on it, or fails at once
// with ErrOutputInUse when another open file holds one. The system frees
// the lock when the file closes.
func openLocked(name string, flags int) (*os.File, error) {
	f, err := os.OpenFile(name, flags, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrOutputInUse
	}
	return err
}

// inUse reports whether another open file holds the lock on name.
func inUse(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	return errors.Is(lockFile(f), ErrOutputInUse)
}
