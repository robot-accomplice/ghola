package transport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// fileURL returns the file:// URL of a local path, as a user writes it.
func fileURL(path string) string {
	return "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}

func fileReq(t *testing.T, method, url, rng string) (*fasthttp.Response, error) {
	t.Helper()
	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(url)
	req.Header.SetMethod(method)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	rsp := fasthttp.AcquireResponse()
	t.Cleanup(func() { fasthttp.ReleaseResponse(rsp) })
	return rsp, tr.Do(context.Background(), req, rsp)
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A file:// URL reads the file, with the headers that resume and -R need.
func TestFileURL_ReadsTheFile(t *testing.T) {
	data := []byte("0123456789")
	rsp, err := fileReq(t, "GET", fileURL(writeTemp(t, data)), "")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := ReadBody(rsp)
	if rsp.StatusCode() != 200 || !bytes.Equal(body, data) {
		t.Fatalf("status %d, body %q", rsp.StatusCode(), body)
	}
	if len(rsp.Header.Peek("Last-Modified")) == 0 || string(rsp.Header.Peek("Accept-Ranges")) != "bytes" {
		t.Error("Last-Modified or Accept-Ranges is missing")
	}
}

// A byte range gives 206 with Content-Range, so -C - and -n work on files.
func TestFileURL_Range(t *testing.T) {
	url := fileURL(writeTemp(t, []byte("0123456789")))
	cases := map[string]struct {
		body, contentRange string
	}{
		"bytes=3-":   {"3456789", "bytes 3-9/10"},
		"bytes=2-4":  {"234", "bytes 2-4/10"},
		"bytes=-2":   {"89", "bytes 8-9/10"},
		"bytes=8-99": {"89", "bytes 8-9/10"},
	}
	for rng, want := range cases {
		rsp, err := fileReq(t, "GET", url, rng)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := ReadBody(rsp)
		if rsp.StatusCode() != 206 || string(body) != want.body || string(rsp.Header.Peek("Content-Range")) != want.contentRange {
			t.Errorf("%s: status %d, body %q, Content-Range %q", rng, rsp.StatusCode(), body, rsp.Header.Peek("Content-Range"))
		}
	}
	if rsp, _ := fileReq(t, "GET", url, "bytes=10-"); rsp.StatusCode() != 416 {
		t.Errorf("a range past the end gives %d, want 416", rsp.StatusCode())
	}
}

// A missing file is ErrFileRead (curl exit 37), not a network error.
func TestFileURL_MissingFile(t *testing.T) {
	_, err := fileReq(t, "GET", fileURL(filepath.Join(t.TempDir(), "none")), "")
	if !errors.Is(err, ErrFileRead) {
		t.Fatalf("err = %v, want ErrFileRead", err)
	}
}

// A folder lists its names, one for each line, as in curl.
func TestFileURL_ListsAFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b"), nil, 0o644) //nolint:errcheck
	os.WriteFile(filepath.Join(dir, "a"), nil, 0o644) //nolint:errcheck
	rsp, err := fileReq(t, "GET", fileURL(dir), "")
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := ReadBody(rsp); string(body) != "a\nb\n" {
		t.Errorf("listing = %q", body)
	}
}

// PUT (ghola -T) writes the file, as curl's upload to file:// does.
func TestFileURL_PutWritesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "up.txt")
	tr, _ := New(&config.Options{}, "")
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(fileURL(path))
	req.Header.SetMethod("PUT")
	req.SetBodyString("uploaded")
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	if err := tr.Do(context.Background(), req, rsp); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "uploaded" {
		t.Errorf("file = %q", b)
	}
}

// A file:// URL for another host is refused: ghola reads only local files.
func TestFileURL_OtherHostRefused(t *testing.T) {
	if _, err := fileReq(t, "GET", "file://server/share/f", ""); err == nil {
		t.Fatal("file://server/... was read")
	}
}
