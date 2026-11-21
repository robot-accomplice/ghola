package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// --input reads the URLs from a file in aria2's format: an option line
// under a URL applies to that URL only (out= goes inside the run's -o
// DIR/, as in aria2). --input-format sitemap and html take the links of
// those files. A bad option line stops the run before any request.
func TestExecute_Input(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("X-A")
		mu.Unlock()
		fmt.Fprint(w, r.URL.Path)
	}))
	defer srv.Close()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lines := write("list.txt", srv.URL+"/a\n  out=first.txt\n# a comment\n"+srv.URL+"/b\n"+srv.URL+"/c\n\theader=X-A: only-c\n")
	out := filepath.Join(dir, "out") + "/"
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := runGhola(t, "download", "-q", "-j", "2", "-o", out, "--input", lines); code != 0 {
		t.Fatalf("lines: exit %d", code)
	}
	for f, want := range map[string]string{"first.txt": "/a", "b": "/b", "c": "/c"} {
		if b, _ := os.ReadFile(filepath.Join(out, f)); string(b) != want {
			t.Errorf("lines: %s = %q, want %q", f, b, want)
		}
	}
	if seen["/c"] != "only-c" || seen["/b"] != "" {
		t.Errorf("headers seen: %v; the option line belongs to /c only", seen)
	}
	sitemap := write("sitemap.xml", `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>`+srv.URL+`/s1</loc></url><url><loc>`+srv.URL+`/s2</loc></url></urlset>`)
	page := write("page.html", `<html><a href="`+srv.URL+`/h1">x</a><img src="`+srv.URL+`/h2"><a href="relative">y</a></html>`)
	for format, file := range map[string]string{"sitemap": sitemap, "html": page} {
		if code := runGhola(t, "download", "-q", "-o", out, "--input", file, "--input-format", format); code != 0 {
			t.Errorf("%s: exit %d", format, code)
		}
	}
	for _, p := range []string{"/s1", "/s2", "/h1", "/h2"} {
		if _, ok := seen[p]; !ok {
			t.Errorf("%s was not fetched", p)
		}
	}
	bad := write("bad.txt", srv.URL+"/never\n  no-such-option=1\n")
	if code := runGhola(t, "download", "-q", "-o", out, "--input", bad); code != 2 {
		t.Errorf("a bad option line: exit %d, want 2", code)
	}
	if _, ok := seen["/never"]; ok {
		t.Error("a request went out before the bad line stopped the run")
	}
}

// --input-format metalink takes each file of a Metalink v4 or v3: the
// best URL by priority fetches it, the others are its mirrors, its name
// goes inside -o DIR/, and its hash checks it, so a wrong file exits 32.
func TestExecute_InputMetalink(t *testing.T) {
	body := bytes.Repeat([]byte("metalink"), 1<<17) // 1 MiB
	sum := sha256.Sum256(body)
	var good, other atomic.Int32
	a := mirror(t, body, 0, false, &good)
	b := mirror(t, body, 0, false, &other)
	dir := t.TempDir()
	out := filepath.Join(dir, "out") + "/"
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	v4 := `<?xml version="1.0"?><metalink xmlns="urn:ietf:params:xml:ns:metalink">
<file name="v4.bin"><hash type="sha-256">` + hex.EncodeToString(sum[:]) + `</hash>
<url priority="2">` + b + `</url><url priority="1">` + a + `</url></file></metalink>`
	v3 := `<?xml version="1.0"?><metalink version="3.0" xmlns="http://www.metalinker.org/"><files>
<file name="v3.bin"><verification><hash type="sha256">` + hex.EncodeToString(make([]byte, 32)) + `</hash></verification>
<resources><url type="http" preference="100">` + a + `</url></resources></file></files></metalink>`
	for name, tc := range map[string]struct {
		doc  string
		want int
	}{"v4": {v4, 0}, "v3, wrong hash": {v3, 32}} {
		f := filepath.Join(dir, name+".meta4")
		os.WriteFile(f, []byte(tc.doc), 0o600) //nolint:errcheck
		if code := runGhola(t, "download", "-q", "-o", out, "--input", f, "--input-format", "metalink"); code != tc.want {
			t.Errorf("%s: exit %d, want %d", name, code, tc.want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(out, "v4.bin")); !bytes.Equal(got, body) {
		t.Errorf("v4.bin: %d bytes", len(got))
	}
	if good.Load() == 0 {
		t.Error("the URL with priority 1 was not used")
	}
}
