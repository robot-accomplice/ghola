package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// mirror serves body with ranges, after delay for each request, and
// counts its requests. broken answers 404.
func mirror(t *testing.T, body []byte, delay time.Duration, broken bool, hits *atomic.Int32) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(delay)
		if broken {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/f"
}

// --source adds mirrors of the same file: the pieces of the download go
// to all of them, and a faster mirror takes more, as aria2 does with
// several URIs. A piece that fails on a mirror goes to another, so one
// broken mirror does not stop the download. The file is the same bytes.
func TestExecute_Source(t *testing.T) {
	body := bytes.Repeat([]byte("0123456789abcdef"), 1<<17) // 2 MiB
	var fast, slow, dead atomic.Int32
	a := mirror(t, body, 0, false, &fast)
	b := mirror(t, body, 150*time.Millisecond, false, &slow)
	dir := t.TempDir()
	if code := runGhola(t, "download", "-q", "-o", filepath.Join(dir, "f"), "--source", b, a); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "f")); !bytes.Equal(got, body) {
		t.Fatalf("the file differs: %d bytes, want %d", len(got), len(body))
	}
	if slow.Load() == 0 || fast.Load() <= slow.Load() {
		t.Errorf("requests: fast %d, slow %d; both must serve, the fast one more", fast.Load(), slow.Load())
	}
	c := mirror(t, body, 0, true, &dead)
	if code := runGhola(t, "download", "-q", "-o", filepath.Join(dir, "g"), "--source", c, a); code != 0 {
		t.Fatalf("a broken mirror: exit %d", code)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "g")); !bytes.Equal(got, body) || dead.Load() == 0 {
		t.Errorf("a broken mirror: %d bytes, %d requests to it", len(got), dead.Load())
	}
}

// A segmented download gives the same bytes with each --file-allocation
// mode that this system has.
func TestExecute_FileAllocation(t *testing.T) {
	body := bytes.Repeat([]byte("ghola-"), 1<<19) // 3 MiB
	var hits atomic.Int32
	u := mirror(t, body, 0, false, &hits)
	for _, mode := range []string{"none", "trunc", "prealloc"} {
		f := filepath.Join(t.TempDir(), mode)
		if code := runGhola(t, "download", "-q", "-n", "4", "--file-allocation", mode, "-o", f, u); code != 0 {
			t.Fatalf("%s: exit %d", mode, code)
		}
		if got, _ := os.ReadFile(f); !bytes.Equal(got, body) {
			t.Errorf("%s: the file differs (%d bytes)", mode, len(got))
		}
	}
}
