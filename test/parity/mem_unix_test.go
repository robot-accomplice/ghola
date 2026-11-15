//go:build parity && !windows

package parity

import (
	"os"
	"runtime"
	"syscall"
)

// startProbe returns a function that reports the peak resident memory of the
// process after it exits.
func startProbe(*os.Process) func(*os.ProcessState) int64 {
	return func(ps *os.ProcessState) int64 {
		ru, ok := ps.SysUsage().(*syscall.Rusage)
		if !ok {
			return 0
		}
		if runtime.GOOS == "darwin" {
			return int64(ru.Maxrss) // bytes on macOS
		}
		return int64(ru.Maxrss) * 1024 // KiB on Linux and the BSDs
	}
}
