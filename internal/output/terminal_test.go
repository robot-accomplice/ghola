package output

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

func asTerminal(t *testing.T, on bool) {
	t.Helper()
	old := isTerminal
	isTerminal = func(io.Writer) bool { return on }
	t.Cleanup(func() { isTerminal = old })
}

func respond(t *testing.T, opts *config.Options, body []byte) (string, error) {
	t.Helper()
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.SetBody(body)
	var out bytes.Buffer
	err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0)
	return out.String(), err
}

var pngStart = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// Binary data to a terminal is refused with a write error (exit 23), as in
// curl: the bytes can change the terminal's state. Nothing is written.
func TestBinaryToTerminal_Refused(t *testing.T) {
	asTerminal(t, true)
	out, err := respond(t, &config.Options{}, pngStart)
	if !errors.Is(err, errBinaryTerminal) || !errors.Is(err, config.ErrWrite) {
		t.Fatalf("err = %v, want the binary-terminal write error", err)
	}
	if out != "" {
		t.Errorf("wrote %q to the terminal", out)
	}
}

// Text to a terminal is not refused.
func TestTextToTerminal_Written(t *testing.T) {
	asTerminal(t, true)
	if out, err := respond(t, &config.Options{}, []byte("hello\n")); err != nil || out != "hello\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// -o - writes binary data to a terminal anyway, as in curl.
func TestBinaryToTerminal_ForcedWithOutputDash(t *testing.T) {
	asTerminal(t, true)
	opts := &config.Options{Output: config.OutputOptions{Stdout: true}}
	if out, err := respond(t, opts, pngStart); err != nil || out != string(pngStart) {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

// A pipe or a file gets binary data unchanged: only a terminal is checked.
func TestBinaryToPipe_Written(t *testing.T) {
	asTerminal(t, false)
	if out, err := respond(t, &config.Options{}, pngStart); err != nil || out != string(pngStart) {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}
