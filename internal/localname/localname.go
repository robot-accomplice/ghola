// Package localname turns a name that a server chose (a Content-Disposition
// file name, the last segment of a URL path, a Metalink name, a mirror path
// segment) into one local file name that stays in its folder.
//
// The rules apply on every OS, because a name that is safe on Linux can be
// a path on Windows: "\" separates folders, "C:" names a drive, "\\host"
// names a share (and makes Windows send NTLM credentials to it), "name:x"
// writes an NTFS stream, and CON, NUL, COM1 and the like open a device.
package localname

import (
	"fmt"
	"runtime"
	"strings"
)

// Rule names for Escape, as --file-names takes them.
const (
	RuleUnix    = "unix"
	RuleWindows = "windows"
	RuleASCII   = "ascii"
)

// windowsForbidden are the characters a Windows file name may not hold.
const windowsForbidden = `<>:"\|?*`

// devices are the Windows device names. A file named after one, with any
// extension, opens the device and not a file.
var devices = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Base returns the last part of raw: the text after the last "/" or "\",
// without a drive prefix such as "C:". It returns "" when nothing usable is
// left ("", ".", ".."), so the caller falls back to its default name.
func Base(raw string) string {
	name := raw
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) >= 2 && name[1] == ':' && isLetter(name[0]) {
		name = name[2:] // C:name is a path relative to drive C
	}
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// onWindows is the OS the file is written on. A test can set it.
var onWindows = runtime.GOOS == "windows"

// SystemRule is the --file-names default: windows on Windows, else unix.
func SystemRule() string {
	if onWindows {
		return RuleWindows
	}
	return RuleUnix
}

// Safe returns Base(raw) escaped with the system rule, or "" when nothing
// usable is left.
func Safe(raw string) string {
	name := Base(raw)
	if name == "" {
		return ""
	}
	return Escape(name, SystemRule())
}

// Escape escapes one path segment so that it names one file in its folder.
// Every rule escapes "/", "\" and NUL, a leading "." or ".." segment, a
// Windows device name, and trailing dots and spaces (Windows drops them,
// so "a.html." would overwrite "a.html"). rule windows also escapes
// <>:"|?* and control characters, and rule ascii every byte outside
// printable ASCII and ":". On Windows ":" is escaped under every rule,
// because there it writes an NTFS stream.
func Escape(name, rule string) string {
	if name == "." || name == ".." {
		return "%2E" + name[1:]
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if forbidden(c, rule) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	out := escapeTrailing(b.String())
	if isDevice(out) {
		out = "%" + fmt.Sprintf("%02X", out[0]) + out[1:]
	}
	return out
}

func forbidden(c byte, rule string) bool {
	if c == '/' || c == '\\' || c == 0 || (c == ':' && onWindows) {
		return true
	}
	switch rule {
	case RuleWindows:
		return c < 0x20 || strings.IndexByte(windowsForbidden, c) >= 0
	case RuleASCII:
		return c < 0x20 || c > 0x7e || c == ':'
	}
	return false
}

// escapeTrailing escapes the dots and spaces at the end of name.
func escapeTrailing(name string) string {
	end := len(name)
	for end > 0 && (name[end-1] == '.' || name[end-1] == ' ') {
		end--
	}
	if end == len(name) {
		return name
	}
	var b strings.Builder
	b.WriteString(name[:end])
	for i := end; i < len(name); i++ {
		fmt.Fprintf(&b, "%%%02X", name[i])
	}
	return b.String()
}

// isDevice reports whether name, without its extension, is a Windows
// device name.
func isDevice(name string) bool {
	stem := name
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	return devices[strings.ToUpper(strings.TrimRight(stem, " "))]
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
