package client

import (
	"fmt"
	"io"
	"os"

	"github.com/robot-accomplice/ghola/internal/config"
)

// allocChunk is the size of each write of zeros for --file-allocation
// prealloc.
const allocChunk = 1 << 20

// reserve reserves the space of a segmented download, as aria2
// --file-allocation does: none writes the pieces where they go and the
// file grows; trunc sets the size at once; prealloc writes zeros, so the
// disk holds the space before the first byte; falloc asks the file system
// for the space (Linux).
func reserve(f *os.File, size int64, mode string) error {
	var err error
	switch mode {
	case "", "none":
		return nil
	case "trunc":
		err = f.Truncate(size)
	case "prealloc":
		zeros := make([]byte, min(size, allocChunk))
		for off := int64(0); off < size && err == nil; off += int64(len(zeros)) {
			_, err = f.WriteAt(zeros[:min(int64(len(zeros)), size-off)], off)
		}
		if err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
	case "falloc":
		err = fallocate(f, size)
	default:
		return fmt.Errorf("--file-allocation %q: use none, prealloc, falloc or trunc", mode)
	}
	if err != nil {
		return fmt.Errorf("--file-allocation %s: %w", mode, config.WriteError(err))
	}
	return nil
}
