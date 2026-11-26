package cli

import (
	"slices"
	"strings"
	"testing"
)

// A repeated list option adds to the list, as wget does with -X a -X b.
// Before, the last one replaced the others, so a folder that the user
// excluded was mirrored.
func TestApply_RepeatedMirrorListsMerge(t *testing.T) {
	c, err := Parse([]string{"mirror", "--exclude-dirs", "/a", "--exclude-dirs", "/b,/c",
		"--include-dirs", "/x", "--include-dirs", "/y", "--accept", "pdf", "--accept", "txt",
		"--reject", "gif", "--reject", "png", "--hosts", "h1", "--hosts", "h2", "http://h1/"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	m := o.Mirror
	for name, c := range map[string][2][]string{
		"exclude-dirs": {m.ExcludeDirs, {"/a", "/b", "/c"}},
		"include-dirs": {m.IncludeDirs, {"/x", "/y"}},
		"accept":       {m.Accept, {"pdf", "txt"}},
		"reject":       {m.Reject, {"gif", "png"}},
		"hosts":        {m.Hosts, {"h1", "h2"}},
	} {
		if !slices.Equal(c[0], c[1]) {
			t.Errorf("--%s: %v, want %v", name, c[0], c[1])
		}
	}
}

// A refused value names its option, so the user knows which one to fix.
func TestApply_RefusedValueNamesTheOption(t *testing.T) {
	c, err := Parse([]string{"mirror", "--cut-dirs", "-1", "http://h/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(c); err == nil || !strings.Contains(err.Error(), "--cut-dirs") {
		t.Errorf("error %v does not name --cut-dirs", err)
	}
}
