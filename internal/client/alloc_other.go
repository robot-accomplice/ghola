//go:build !linux

package client

import (
	"errors"
	"os"
)

// fallocate is Linux only; elsewhere --file-allocation falloc is an error,
// so the space is not silently left unreserved.
func fallocate(*os.File, int64) error {
	return errors.New("falloc needs Linux: use prealloc or trunc")
}
