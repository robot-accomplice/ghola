package client

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Each --file-allocation mode reserves what aria2's does: none nothing,
// trunc and prealloc the whole size; falloc is Linux only and an error
// elsewhere, so the space is never silently left unreserved.
func TestReserve(t *testing.T) {
	const size = 3<<20 + 5
	for mode, want := range map[string]int64{"none": 0, "trunc": size, "prealloc": size, "falloc": size} {
		f, err := os.Create(filepath.Join(t.TempDir(), mode))
		if err != nil {
			t.Fatal(err)
		}
		err = reserve(f, size, mode)
		fi, _ := f.Stat()
		f.Close()
		if mode == "falloc" && runtime.GOOS != "linux" {
			if err == nil {
				t.Error("falloc outside Linux was accepted")
			}
			continue
		}
		if err != nil || fi.Size() != want {
			t.Errorf("%s: size %d, err %v; want %d", mode, fi.Size(), err, want)
		}
	}
	if err := reserve(nil, 1, "sparse"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}
