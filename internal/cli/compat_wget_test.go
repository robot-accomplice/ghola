package cli

import (
	"strings"
	"testing"
)

// wget -nc keeps an existing file: --no-output-overwrite. Before, ghola
// wget -nc was an argument error.
func TestWget_NoClobber(t *testing.T) {
	for _, flag := range []string{"-nc", "--no-clobber"} {
		if got := native(t, FromWget, flag, "u"); !strings.Contains(got, " --no-output-overwrite ") {
			t.Errorf("%s: %s", flag, got)
		}
	}
}

// --restrict-file-names takes the values that native --file-names takes;
// another value is an error in ghola wget, so --show-native never prints
// a command that fails.
func TestWget_RestrictFileNames(t *testing.T) {
	for _, v := range []string{"unix", "windows", "ascii"} {
		if got := native(t, FromWget, "-r", "--restrict-file-names="+v, "u"); !strings.Contains(got, " --file-names "+v+" ") {
			t.Errorf("%s: %s", v, got)
		}
	}
	for _, v := range []string{"lowercase", "unix,nocontrol", "nocontrol"} {
		c, err := FromWget([]string{"-r", "--restrict-file-names=" + v, "u"})
		if err == nil {
			t.Errorf("%s: no error, native %q", v, c.Args)
			continue
		}
		if !strings.Contains(err.Error(), "is not one of unix, windows, ascii") {
			t.Errorf("%s: %v", v, err)
		}
	}
}
