package output

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// With the v1 --fail rule, a status of 400 or above exits 22, and stdout
// still gets the body (spec 4.2, as curl --fail-with-body). An API error
// body tells the user why the request failed; ghola wrote 0 bytes.
func TestStdoutKeepsTheBodyOfARefusedStatus(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(404)
	rsp.SetBodyString(`{"error":"no such item"}`)
	var out bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Fail: true, FailFrom400: true}}
	err := ProcessResponse(&out, io.Discard, opts, nil, rsp, 0)
	if !errors.Is(err, config.ErrHTTPStatus) {
		t.Fatalf("err = %v, want the HTTP status error (exit 22)", err)
	}
	if out.String() != `{"error":"no such item"}` {
		t.Errorf("stdout = %q, want the error body", out.String())
	}
}

// -o writes no file for a refused status: a file named for the content
// must not hold an error page.
func TestFileGetsNoBodyOfARefusedStatus(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(500)
	rsp.SetBodyString("error page")
	name := filepath.Join(t.TempDir(), "out.bin")
	opts := &config.Options{Output: config.OutputOptions{Fail: true, FailFrom400: true, File: name}}
	if err := ProcessResponse(io.Discard, io.Discard, opts, nil, rsp, 0); !errors.Is(err, config.ErrHTTPStatus) {
		t.Fatalf("err = %v, want the HTTP status error", err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Errorf("the output file exists (%v), want no file", err)
	}
}
