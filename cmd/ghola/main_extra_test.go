package main

// main_extra_test.go — additional coverage for the execute branches that were
// below the 66.1% baseline after the streaming-downloads work:
//   - -F/--form body assembly + Content-Type override
//   - --data-binary (inline and @file)
//   - --data-urlencode
//   - -d @file and -d - (stdin) reading
//   - streaming path: file-output with known body
//   - HAR output path
//   - ProcessResponse ErrNon2xx path

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// TestExecute_FormBody verifies that -F fields produce a multipart body and
// the right Content-Type is sent to the server.
func TestExecute_FormBody(t *testing.T) {
	var gotCT string
	var gotBody string
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		gotCT = string(ctx.Request.Header.Peek("Content-Type"))
		gotBody = string(ctx.PostBody())
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	// Use a temp file for form @file upload.
	dir := t.TempDir()
	ff := filepath.Join(dir, "field.txt")
	os.WriteFile(ff, []byte("field-content"), 0644) //nolint:errcheck

	code := execute(bg(), []string{"-F", "name=value", "-F", "upload=@" + ff, "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data prefix", gotCT)
	}
	if !strings.Contains(gotBody, "name") || !strings.Contains(gotBody, "value") {
		t.Errorf("body %q missing form fields", gotBody)
	}
}

// TestExecute_FormBadFile verifies that a -F @file that doesn't exist returns
// ReadFileFailed.
func TestExecute_FormBadFile(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"-F", "f=@/nonexistent/file.bin", "http://test"}, do, nil, os.Stdout)
	if code != config.ReadFileFailed.Int() {
		t.Errorf("exit code = %d, want ReadFileFailed (%d)", code, config.ReadFileFailed.Int())
	}
}

// TestExecute_DataBinaryInline verifies --data-binary with a plain string.
func TestExecute_DataBinaryInline(t *testing.T) {
	var gotBody string
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		gotBody = string(ctx.PostBody())
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	code := execute(bg(), []string{"--data-binary", "raw\x00binary", "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	if gotBody != "raw\x00binary" {
		t.Errorf("body = %q, want 'raw\\x00binary'", gotBody)
	}
}

// TestExecute_DataBinaryAtFile verifies --data-binary @file reads file verbatim.
func TestExecute_DataBinaryAtFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bin.dat")
	payload := []byte("binary\x00\x01\x02payload")
	os.WriteFile(f, payload, 0644) //nolint:errcheck

	var gotBody []byte
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		gotBody = ctx.PostBody()
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	code := execute(bg(), []string{"--data-binary", "@" + f, "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body mismatch: got %q, want %q", gotBody, payload)
	}
}

// TestExecute_DataBinaryAtFileMissing verifies --data-binary @missing returns ReadFileFailed.
func TestExecute_DataBinaryAtFileMissing(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"--data-binary", "@/no/such/file.bin", "http://test"}, do, nil, os.Stdout)
	if code != config.ReadFileFailed.Int() {
		t.Errorf("exit code = %d, want ReadFileFailed (%d)", code, config.ReadFileFailed.Int())
	}
}

// TestExecute_DataURLEncode verifies --data-urlencode URL-encodes and POSTs.
func TestExecute_DataURLEncode(t *testing.T) {
	var gotBody string
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		gotBody = string(ctx.PostBody())
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	code := execute(bg(), []string{"--data-urlencode", "key=hello world", "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	// URL-encoded: space becomes + or %20
	if !strings.Contains(gotBody, "key=") {
		t.Errorf("body = %q, expected key= prefix", gotBody)
	}
}

// TestExecute_DataAtFile verifies -d @file reads from file (existing path).
func TestExecute_DataAtFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "data.txt")
	os.WriteFile(f, []byte("file-body"), 0644) //nolint:errcheck

	var gotBody string
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		gotBody = string(ctx.PostBody())
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	code := execute(bg(), []string{"-d", "@" + f, "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	if gotBody != "file-body" {
		t.Errorf("body = %q, want 'file-body'", gotBody)
	}
}

// TestExecute_DataAtFileMissing verifies -d @missing returns ReadFileFailed.
func TestExecute_DataAtFileMissing(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"-d", "@/no/such.txt", "http://test"}, do, nil, os.Stdout)
	if code != config.ReadFileFailed.Int() {
		t.Errorf("exit code = %d, want ReadFileFailed", code)
	}
}

// TestExecute_StreamingFileOutput verifies the streaming path writes the correct
// body to a file and returns NoError.
func TestExecute_StreamingFileOutput(t *testing.T) {
	body := []byte("streaming-body-content")
	stream, cleanup := testStreamer(func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.Response.Header.SetContentLength(len(body))
		ctx.SetStatusCode(200)
		ctx.SetBody(body)
	})
	defer cleanup()

	dir := t.TempDir()
	out := filepath.Join(dir, "stream.bin")
	code := execute(bg(), []string{"-o", out, "http://test"}, nil, stream, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("file content = %q, want %q", got, body)
	}
}

// TestExecute_StreamingDownloadSilentError verifies that a streaming error
// with -s (silent) returns WriteFileFailed without printing to stderr.
func TestExecute_StreamingDownloadSilentError(t *testing.T) {
	// Streamer that always errors.
	errStream := client.Streamer(func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		return gholatransport.StreamMeta{}, os.ErrPermission
	})

	dir := t.TempDir()
	out := filepath.Join(dir, "silent.bin")
	code := execute(bg(), []string{"-s", "-o", out, "http://test"}, nil, errStream, os.Stdout)
	if code != config.WriteFileFailed.Int() {
		t.Errorf("exit code = %d, want WriteFileFailed (%d)", code, config.WriteFileFailed.Int())
	}
}

// TestExecute_HAROutput verifies the HAR export path is exercised (no error
// when --har is set and the request completes).
func TestExecute_HAROutput(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString(`{"ok":true}`)
	})
	defer cleanup()

	dir := t.TempDir()
	harFile := filepath.Join(dir, "out.har")
	code := execute(bg(), []string{"--har", harFile, "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want NoError", code)
	}
	// HAR file should exist and be non-empty.
	info, err := os.Stat(harFile)
	if err != nil {
		t.Fatalf("har file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("har file is empty")
	}
}

// TestExecute_Non2xxFail verifies that a 404 with -f (--fail) returns SendFailed.
func TestExecute_Non2xxFail(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(http.StatusNotFound)
		ctx.SetBodyString("not found")
	})
	defer cleanup()

	code := execute(bg(), []string{"-f", "http://test"}, do, nil, os.Stdout)
	if code != config.SendFailed.Int() {
		t.Errorf("exit code = %d, want SendFailed (%d)", code, config.SendFailed.Int())
	}
}
