package client

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// statusStreamer answers every request with status and body.
func statusStreamer(status int, body string) Streamer {
	return func(_ context.Context, _ *config.Options, _ *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		meta := gholatransport.StreamMeta{StatusCode: status, ContentLength: int64(len(body))}
		return meta, gholatransport.CopyBody(sink, meta, strings.NewReader(body))
	}
}

// -f -o on an HTTP error writes no file, as curl -f -o does. Before the fix
// the 404 body went into the file (audit finding H7).
func TestDownload_FailWritesNoFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f")
	opts := &config.Options{URL: "http://test/f", Method: "GET", Concurrency: 1,
		Output: config.OutputOptions{File: out, Fail: true, Silent: true}}

	err := Download(context.Background(), opts, statusStreamer(404, "not found body"))
	if !errors.Is(err, config.ErrHTTPStatus) {
		t.Fatalf("err = %v, want %v", err, config.ErrHTTPStatus)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Errorf("the file exists after -f on a 404 (stat err %v)", serr)
	}
}

// A refused response leaves an existing file as it was. The file was opened
// with O_TRUNC before the response arrived, so -f on a 404 emptied it.
func TestDownload_FailKeepsAnExistingFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(out, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := &config.Options{URL: "http://test/f", Method: "GET", Concurrency: 1,
		Output: config.OutputOptions{File: out, Fail: true, Silent: true}}

	if err := Download(context.Background(), opts, statusStreamer(500, "error page")); err == nil {
		t.Fatal("no error for a 500 with -f")
	}
	if got, _ := os.ReadFile(out); string(got) != "keep me" {
		t.Errorf("file = %q, want it unchanged", got)
	}
}

// An empty 200 body still creates the (empty) file, as curl does: the file
// opens when the response is accepted, not on the first byte.
func TestDownload_EmptyBodyCreatesTheFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f")
	opts := &config.Options{URL: "http://test/f", Method: "GET", Concurrency: 1,
		Output: config.OutputOptions{File: out, Silent: true}}

	if err := Download(context.Background(), opts, statusStreamer(200, "")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() != 0 {
		t.Errorf("stat = %v, %v; want an empty file", info, err)
	}
}

// -L stops at --max-redirs with an error, as curl does (exit 47). Before
// the fix FetchURL returned the last 3xx as a success, and ghola exited 0
// (audit finding H6).
func TestFetchURL_RedirectLimitIsAnError(t *testing.T) {
	var hops int
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		hops++
		ctx.SetStatusCode(302)
		ctx.Response.Header.Set("Location", "/next")
	})
	defer ln.Close()

	opts := &config.Options{URL: "http://test/start", Method: "GET",
		Resilience: config.ResilienceOptions{Location: true, MaxRedirs: 3},
		Output:     config.OutputOptions{Silent: true}}
	req, rsp, err := FetchURL(bg(), opts, do)
	if !errors.Is(err, ErrTooManyRedirects) || req != nil || rsp != nil {
		t.Fatalf("FetchURL = %v, %v, %v; want nil, nil, %v", req, rsp, err, ErrTooManyRedirects)
	}
	if hops != 4 {
		t.Errorf("server saw %d requests, want 4 (the first and 3 redirects)", hops)
	}
}
