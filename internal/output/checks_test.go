package output

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/robot-accomplice/ghola/internal/config"
)

func answer(t *testing.T, status int, body string, headers map[string]string) (*fasthttp.Request, *fasthttp.Response) {
	t.Helper()
	req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	t.Cleanup(func() { fasthttp.ReleaseRequest(req); fasthttp.ReleaseResponse(rsp) })
	req.SetRequestURI("http://h/x")
	rsp.SetStatusCode(status)
	for k, v := range headers {
		rsp.Header.Set(k, v)
	}
	rsp.SetBodyString(body)
	return req, rsp
}

// On stdout too, a body that does not match the server's Content-Digest
// fails with ErrChecksum (exit 32), and one that matches passes. The
// count of written bytes reaches the template.
func TestProcessResponse_ContentDigest(t *testing.T) {
	sum := sha256.Sum256([]byte("body"))
	good := "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
	info := &config.TransferInfo{}
	opts := &config.Options{Output: config.OutputOptions{Info: info}}
	req, rsp := answer(t, 200, "body", map[string]string{"Content-Digest": good})
	var out bytes.Buffer
	if err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0); err != nil || out.String() != "body" || info.Size != 4 {
		t.Errorf("a good digest: %v %q size %d", err, out.String(), info.Size)
	}
	req, rsp = answer(t, 200, "bodY", map[string]string{"Content-Digest": good})
	if err := ProcessResponse(io.Discard, io.Discard, opts, req, rsp, 0); !errors.Is(err, config.ErrChecksum) {
		t.Errorf("a bad body: %v", err)
	}
}

// --max-size refuses a declared length above the size before any byte
// goes out, so a huge body never reaches stdout.
func TestProcessResponse_MaxSize(t *testing.T) {
	opts := &config.Options{Output: config.OutputOptions{MaxSize: 3}}
	req, rsp := answer(t, 200, "four", nil)
	var out bytes.Buffer
	if err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0); !errors.Is(err, config.ErrTooLarge) || out.Len() != 0 {
		t.Errorf("%v, %d bytes out", err, out.Len())
	}
}

// A 304 to a condition (--cond-time, --cond-etag-compare) writes no file,
// as curl -z; a 200 saves the ETag for the next run.
func TestProcessResponse_ConditionsAndETag(t *testing.T) {
	dir := t.TempDir()
	file, etag := filepath.Join(dir, "f"), filepath.Join(dir, "etag")
	opts := &config.Options{Output: config.OutputOptions{File: file, CondTime: true, ETagSave: etag}}
	req, rsp := answer(t, 304, "", map[string]string{"ETag": `"v2"`})
	if err := ProcessResponse(io.Discard, io.Discard, opts, req, rsp, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("a 304 wrote the file")
	}
	req, rsp = answer(t, 200, "new", map[string]string{"ETag": `"v3"`})
	if err := ProcessResponse(io.Discard, io.Discard, opts, req, rsp, 0); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "new" {
		t.Errorf("file %q", b)
	}
	if b, _ := os.ReadFile(etag); string(b) != "\"v3\"\n" {
		t.Errorf("etag %q", b)
	}
}
