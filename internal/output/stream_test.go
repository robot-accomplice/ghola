package output

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// stdout must carry the body exactly as the server sent it. The old code
// trimmed every leading and trailing newline and added one, so a binary file
// grew by a byte and a text file lost its blank lines (audit finding H2).
func TestProcessResponse_StdoutIsByteExact(t *testing.T) {
	for _, body := range []string{
		"",
		"no newline",
		"\n\nblank lines around\n\n",
		"\x00\x01binary\n\x0a\xff",
	} {
		req := fasthttp.AcquireRequest()
		rsp := fasthttp.AcquireResponse()
		rsp.SetStatusCode(200)
		rsp.SetBodyString(body)

		var out bytes.Buffer
		err := ProcessResponse(&out, io.Discard, &config.Options{}, req, rsp, 0)
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(rsp)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if out.String() != body {
			t.Errorf("stdout = %q, want %q", out.String(), body)
		}
	}
}

// -i puts the response header block on stdout in front of the body, byte for
// byte, as curl -i does.
func TestProcessResponse_IncludeWritesTheExactHeaderBlock(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.Header.Set("X-Test", "v")
	rsp.SetBodyString("body\n")
	head := string(rsp.Header.Header())

	var out bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Include: true}}
	if err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0); err != nil {
		t.Fatal(err)
	}
	if want := head + "body\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

// A gzip body decoded for --compressed keeps its last newline (the old Trim
// removed it).
func TestProcessResponse_CompressedStreamIsByteExact(t *testing.T) {
	plain := "line one\nline two\n\n"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(plain)) //nolint:errcheck
	zw.Close()              //nolint:errcheck

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.Header.Set("Content-Encoding", "gzip")
	rsp.SetBodyStream(bytes.NewReader(gz.Bytes()), gz.Len())

	var out bytes.Buffer
	opts := &config.Options{Stealth: config.StealthOptions{Compressed: true}}
	if err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0); err != nil {
		t.Fatal(err)
	}
	if out.String() != plain {
		t.Errorf("stdout = %q, want %q", out.String(), plain)
	}
}

// signalWriter records what it receives and reports the first write.
type signalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.once.Do(func() { close(w.first) })
	return w.buf.Write(p)
}

// The body reaches stdout while the server is still sending it. The old code
// held the whole body in memory first, so a body larger than memory failed
// (audit finding M2). The server here sends the second half only after the
// first half is on stdout: a buffering implementation never gets there.
func TestProcessResponse_StreamsBodyToStdout(t *testing.T) {
	pr, pw := io.Pipe()
	out := &signalWriter{first: make(chan struct{})}
	go func() {
		pw.Write([]byte("first half,")) //nolint:errcheck
		select {
		case <-out.first:
			pw.Write([]byte("second half")) //nolint:errcheck
			pw.Close()
		case <-time.After(5 * time.Second):
			pw.CloseWithError(errors.New("stdout got nothing before the body ended: the body is buffered"))
		}
	}()

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.SetBodyStream(pr, -1)

	if err := ProcessResponse(out, io.Discard, &config.Options{}, req, rsp, 0); err != nil {
		t.Fatal(err)
	}
	if got := out.buf.String(); got != "first half,second half" {
		t.Errorf("stdout = %q", got)
	}
}

// A body that breaks off is an error. It must not end as a short stdout and a
// zero exit status.
func TestProcessResponse_ReportsABrokenBody(t *testing.T) {
	broken := errors.New("connection reset")
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.SetBodyStream(io.MultiReader(bytes.NewReader([]byte("part")), errReader{broken}), -1)

	var out bytes.Buffer
	err := ProcessResponse(&out, io.Discard, &config.Options{}, req, rsp, 0)
	if !errors.Is(err, broken) {
		t.Fatalf("err = %v, want %v", err, broken)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
