package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableANSI turns on virtual terminal processing for the console behind f,
// so the console renders ANSI codes instead of printing them. It returns
// false when f is not a console or the console does not support it.
func enableANSI(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
