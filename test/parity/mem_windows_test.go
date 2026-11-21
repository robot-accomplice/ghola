//go:build parity && windows

package parity

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS from psapi.h.
type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// startProbe opens a second handle to the process, so its counters can be
// read after it exits. It returns a function that reports the peak working set.
func startProbe(p *os.Process) func(*os.ProcessState) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.Pid))
	if err != nil {
		return func(*os.ProcessState) int64 { return 0 }
	}
	return func(*os.ProcessState) int64 {
		defer func() { _ = windows.CloseHandle(h) }()
		var c processMemoryCounters
		c.cb = uint32(unsafe.Sizeof(c))
		ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
		if ok == 0 {
			return 0
		}
		return int64(c.PeakWorkingSetSize)
	}
}
