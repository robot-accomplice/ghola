package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func testDoer(handler fasthttp.RequestHandler) (client.Doer, func()) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: handler}
	go srv.Serve(ln) //nolint:errcheck

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) {
			return ln.Dial()
		},
	}
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return c.Do(req, resp)
	}, func() { ln.Close() }
}

// testStreamer returns a Streamer backed by the same handler as testDoer, for
// use by tests that exercise the streaming download path (-o file + GET).
func testStreamer(handler fasthttp.RequestHandler) (client.Streamer, func()) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: handler}
	go srv.Serve(ln) //nolint:errcheck

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial: func(addr string) (net.Conn, error) {
			return ln.Dial()
		},
	}
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		rsp.StreamBody = true
		if err := c.Do(req, rsp); err != nil {
			return gholatransport.StreamMeta{}, err
		}
		meta := gholatransport.StreamMeta{
			StatusCode:    rsp.Header.StatusCode(),
			ContentLength: int64(rsp.Header.ContentLength()),
			AcceptRanges:  bytes.EqualFold(rsp.Header.Peek("Accept-Ranges"), []byte("bytes")),
			LastModified:  string(rsp.Header.Peek("Last-Modified")),
			Disposition:   string(rsp.Header.Peek("Content-Disposition")),
		}
		if err := rsp.BodyWriteTo(sink); err != nil {
			return meta, err
		}
		return meta, nil
	}, func() { ln.Close() }
}

func bg() context.Context { return context.Background() }

func TestExecute_BasicGET(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("hello")
	})
	defer cleanup()

	code := execute(bg(), []string{"http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}
}

func TestExecute_BadFlag(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{}, do, nil, os.Stdout)
	if code != config.BadFlag.Int() {
		t.Errorf("exit code = %d, want %d (BadFlag)", code, config.BadFlag.Int())
	}
}

func TestExecute_Help(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"--help"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d (NoError for help)", code, config.NoError.Int())
	}
}

func TestExecute_Version(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"--version"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d (NoError for version)", code, config.NoError.Int())
	}
}

func TestExecute_SendFailed(t *testing.T) {
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return &net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: os.ErrNotExist}}
	}

	code := execute(bg(), []string{"http://test"}, failDoer, nil, os.Stdout)
	if code != config.SendFailed.Int() {
		t.Errorf("exit code = %d, want %d (SendFailed)", code, config.SendFailed.Int())
	}
}

func TestExecute_SendFailedSilent(t *testing.T) {
	failDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		return &net.OpError{Op: "dial", Err: &os.SyscallError{Syscall: "connect", Err: os.ErrNotExist}}
	}

	code := execute(bg(), []string{"-s", "http://test"}, failDoer, nil, os.Stdout)
	if code != config.SendFailed.Int() {
		t.Errorf("exit code = %d, want %d (SendFailed)", code, config.SendFailed.Int())
	}
}

func TestExecute_SnoopMode(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Server", "nginx")
	})
	defer cleanup()

	code := execute(bg(), []string{"-S", "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}
}

func TestExecute_FileOutput(t *testing.T) {
	stream, cleanup := testStreamer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("file data")
	})
	defer cleanup()

	outFile := filepath.Join(t.TempDir(), "out.txt")
	code := execute(bg(), []string{"-o", outFile, "http://test"}, nil, stream, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "file data" {
		t.Errorf("file content = %q, want 'file data'", data)
	}
}

func TestExecute_UploadFile(t *testing.T) {
	uploadFile := filepath.Join(t.TempDir(), "upload.txt")
	os.WriteFile(uploadFile, []byte("upload content"), 0644)

	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.PostBody()) != "upload content" {
			t.Errorf("body = %q, want 'upload content'", ctx.PostBody())
		}
		ctx.SetStatusCode(200)
	})
	defer cleanup()

	code := execute(bg(), []string{"-T", uploadFile, "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}
}

func TestExecute_UploadFileNotFound(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {})
	defer cleanup()

	code := execute(bg(), []string{"-T", "/nonexistent/file.txt", "http://test"}, do, nil, os.Stdout)
	if code != config.ReadFileFailed.Int() {
		t.Errorf("exit code = %d, want %d (ReadFileFailed)", code, config.ReadFileFailed.Int())
	}
}

func TestExecute_WriteFileFailed(t *testing.T) {
	stream, cleanup := testStreamer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("data")
	})
	defer cleanup()

	code := execute(bg(), []string{"-o", "/nonexistent/dir/file.txt", "http://test"}, nil, stream, os.Stdout)
	if code != config.WriteFileFailed.Int() {
		t.Errorf("exit code = %d, want %d (WriteFileFailed)", code, config.WriteFileFailed.Int())
	}
}

func TestExecute_ConcurrentMode(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("concurrent")
	})
	defer cleanup()

	code := execute(bg(), []string{"-n", "3", "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}
}

func TestExecute_ConcurrentSnoop(t *testing.T) {
	do, cleanup := testDoer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Server", "cloudflare")
	})
	defer cleanup()

	code := execute(bg(), []string{"-n", "2", "-S", "http://test"}, do, nil, os.Stdout)
	if code != config.NoError.Int() {
		t.Errorf("exit code = %d, want %d", code, config.NoError.Int())
	}
}
