package client

import (
	"os"

	"golang.org/x/sys/unix"
)

// fallocate asks the file system for the space of the file.
func fallocate(f *os.File, size int64) error {
	return unix.Fallocate(int(f.Fd()), 0, 0, size)
}
