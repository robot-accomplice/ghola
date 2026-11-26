package config

import (
	"os"
	"path/filepath"
	"testing"
)

// -o DIR/ names a folder for the remote file name, so ghola makes that one
// folder, as aria2c -d does (decision 2026-11-25). A missing parent folder
// still needs --output-create-dirs: a typo in a path must not build a tree.
func TestOpenOutput_DirTargetCreatesThatFolder(t *testing.T) {
	base := t.TempDir()
	var o OutputOptions
	o.SetTarget(filepath.Join(base, "new") + "/")
	f, err := OpenOutput(o, filepath.Join(o.Dir, "a.bin"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false)
	if err != nil {
		t.Fatalf("-o new/: %v", err)
	}
	f.Close()

	o.SetTarget(filepath.Join(base, "missing", "new") + "/")
	if f, err := OpenOutput(o, filepath.Join(o.Dir, "a.bin"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false); err == nil {
		f.Close()
		t.Fatal("-o missing/new/ made the missing parent folder")
	}
}
