package localname

import "testing"

// A server picks these names. Each one must stay one file in its folder on
// Windows: no folder climb, no drive, no share, no NTFS stream, no device.
func TestSafe_ServerNamesStayInTheFolder(t *testing.T) {
	old := onWindows
	onWindows = true
	defer func() { onWindows = old }()
	for raw, want := range map[string]string{
		`report.pdf`:          "report.pdf",
		`..\..\up.txt`:        "up.txt",
		`../../up.txt`:        "up.txt",
		`C:\Users\x\abs.txt`:  "abs.txt",
		`C:..\drive-rel.txt`:  "drive-rel.txt",
		`C:drv.txt`:           "drv.txt",
		`\host\share\unc.txt`: "unc.txt",
		`ads.txt:hidden`:      "ads.txt%3Ahidden",
		`CON`:                 "%43ON",
		`nul.txt`:             "%6Eul.txt",
		`COM1.bin`:            "%43OM1.bin",
		`trail.html.`:         "trail.html%2E",
		`space `:              "space%20",
		`a&b.bin`:             "a&b.bin",
		`..`:                  "",
		``:                    "",
	} {
		if got := Safe(raw); got != want {
			t.Errorf("Safe(%q) = %q, want %q", raw, got, want)
		}
	}
}

// On Linux the unix rule keeps the characters that are legal there, but a
// backslash still never reaches a path: the same name must not escape on
// Windows either.
func TestEscape_UnixRuleKeepsLegalBytes(t *testing.T) {
	old := onWindows
	onWindows = false
	defer func() { onWindows = old }()
	for in, want := range map[string]string{`a:b?.txt`: "a:b?.txt", `x\..\y`: "x%5C..%5Cy", `..`: "%2E."} {
		if got := Escape(in, RuleUnix); got != want {
			t.Errorf("Escape(%q, unix) = %q, want %q", in, got, want)
		}
	}
}

// On Windows every rule escapes ":" and "\", so --file-names unix or ascii
// cannot write a stream or climb out of the copy.
func TestEscape_WindowsEveryRule(t *testing.T) {
	old := onWindows
	onWindows = true
	defer func() { onWindows = old }()
	for _, rule := range []string{RuleUnix, RuleWindows, RuleASCII} {
		if got := Escape(`..\x:y`, rule); got != "..%5Cx%3Ay" {
			t.Errorf("rule %s: %q", rule, got)
		}
	}
}
