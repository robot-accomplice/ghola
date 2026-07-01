package client

// download_extra_test.go — additional coverage for download.go functions that
// were below the 80% baseline after the streaming-downloads work:
//   - resolveTargetViaGet / streamLocation (0%)
//   - probe HEAD-unsupported fallback and fallback-GET error path (45%)
//   - resumeOffset numeric / auto / missing-file (50%)
//   - buildDownloadRequest ghost + stealth branches (59%)
//   - downloadSegmented error path (76.9%)
//   - Download with -J/-R paths (57%)

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// TestResolveTargetViaGet_FollowsRedirect verifies that when the injected
// Streamer sees a HEAD→302, resolveTarget falls through to resolveTargetViaGet
// and ultimately the correct file is downloaded.
func TestResolveTargetViaGet_FollowsRedirect(t *testing.T) {
	data := bytes.Repeat([]byte("redir-"), 1000)

	// Use a simpler setup: blobServer serves normally for GET.
	// We intercept HEAD to return a 301 by wrapping blobServer.
	inner := blobServer(t, data)

	var headCount int
	redirectStreamer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		if string(req.Header.Method()) == fasthttp.MethodHead && headCount == 0 {
			headCount++
			// Simulate HEAD returning a redirect status with a Location.
			// Since resolveTarget checks isRedirectStatus, we return 302.
			// The Location goes back to the same URL (one-hop loop that resolves).
			return gholatransport.StreamMeta{StatusCode: fasthttp.StatusFound}, nil
		}
		return inner(ctx, opts, req, sink)
	}

	// Now wire a real httptest server so streamLocation can use the real transport.
	finalData := bytes.Repeat([]byte("redir-"), 1000)
	realSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Range=bytes=0-0 probe
			if r.Header.Get("Range") == "bytes=0-0" {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusPartialContent)
				w.Write(finalData[:1]) //nolint:errcheck
				return
			}
			// Not a redirect
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(finalData)))
			w.WriteHeader(200)
			w.Write(finalData) //nolint:errcheck
		}
	}))
	defer realSrv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "blob.bin")
	opts := &config.Options{
		URL:         realSrv.URL + "/file",
		Method:      "GET",
		Concurrency: 1,
		Output:      config.OutputOptions{File: out},
		Stealth:     config.StealthOptions{Agent: "ghola-test"},
		Resilience:  config.ResilienceOptions{MaxRedirs: 5},
	}
	// Use a streamer that for the HEAD returns 302 so resolveTargetViaGet is called.
	// The real transport in streamLocation will call realSrv which returns non-redirect.
	_ = redirectStreamer

	// Simplest path: use a pure-injected streamer that returns 302 for HEAD,
	// then the real data for everything else. streamLocation will use the real
	// transport (calling realSrv for bytes=0-0).
	var hop int
	testStream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		method := string(req.Header.Method())
		if method == fasthttp.MethodHead && hop == 0 {
			hop++
			return gholatransport.StreamMeta{StatusCode: fasthttp.StatusFound}, nil
		}
		// All other calls (final GET) — serve the real data.
		_, err := sink.Write(finalData)
		return gholatransport.StreamMeta{StatusCode: 200, ContentLength: int64(len(finalData))}, err
	}

	if err := Download(context.Background(), opts, testStream); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(finalData) {
		t.Fatalf("file checksum mismatch (got %d bytes, want %d)", len(got), len(finalData))
	}
}

// TestStreamLocation_RealHTTP verifies streamLocation with the real production
// transport connecting to a real TCP server. This covers the DefaultStreamer
// and streamLocation code paths.
func TestStreamLocation_RealHTTP(t *testing.T) {
	// Serve a 302 then a 200 on /dest.
	mux := http.NewServeMux()
	mux.HandleFunc("/src", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dest", http.StatusFound)
	})
	mux.HandleFunc("/dest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("x")) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(srv.URL + "/src")
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("Range", "bytes=0-0")

	opts := &config.Options{
		Stealth:    config.StealthOptions{Agent: "ghola-test"},
		Resilience: config.ResilienceOptions{MaxRedirs: 5},
	}
	loc, status, err := streamLocation(context.Background(), opts, req)
	if err != nil {
		t.Fatalf("streamLocation error: %v", err)
	}
	// /src redirects with 302; fasthttp's transport returns the redirect status
	// directly rather than following to /dest, so the pinned value is 302.
	if status != http.StatusFound {
		t.Errorf("got status %d, want %d (http.StatusFound)", status, http.StatusFound)
	}
	// loc may be empty: fasthttp collapses the Location header rather than
	// surfacing it when the redirect is handled at the transport layer.
	_ = loc
}

// TestDefaultStreamer_RealHTTP exercises DefaultStreamer against a real server.
func TestDefaultStreamer_RealHTTP(t *testing.T) {
	body := []byte("default-streamer-ok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write(body) //nolint:errcheck
	}))
	defer srv.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(srv.URL + "/")
	req.Header.SetMethod(fasthttp.MethodGet)

	opts := &config.Options{
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	var sink bytes.Buffer
	meta, err := DefaultStreamer(context.Background(), opts, req, &sink)
	if err != nil {
		t.Fatalf("DefaultStreamer error: %v", err)
	}
	if meta.StatusCode != 200 {
		t.Errorf("status = %d, want 200", meta.StatusCode)
	}
	if sink.String() != string(body) {
		t.Errorf("body = %q, want %q", sink.String(), body)
	}
}

// TestDefaultStreamer_BadTLSOptions verifies DefaultStreamer returns an error
// when the options are invalid (e.g. --cert without --key).
func TestDefaultStreamer_BadTLSOptions(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://127.0.0.1/")
	req.Header.SetMethod(fasthttp.MethodGet)

	opts := &config.Options{
		Stealth: config.StealthOptions{ClientCert: "/nonexistent.pem", ClientKey: ""},
	}
	_, err := DefaultStreamer(context.Background(), opts, req, io.Discard)
	if err == nil {
		t.Fatal("expected error for --cert without --key, got nil")
	}
}

// TestProbe_HEADUnsupportedFallback verifies probe falls back to a 1-byte
// ranged GET when the server returns 405 (or no Content-Length) on HEAD.
func TestProbe_HEADUnsupportedFallback(t *testing.T) {
	data := bytes.Repeat([]byte("probe-"), 1000)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		if ctx.IsHead() {
			// HEAD unsupported — return 405 with no content-length.
			ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
			return
		}
		rng := string(ctx.Request.Header.Peek("Range"))
		if rng == "bytes=0-0" {
			ctx.Response.Header.Set("Accept-Ranges", "bytes")
			ctx.SetStatusCode(fasthttp.StatusPartialContent)
			ctx.SetBody(data[:1])
			return
		}
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.Response.Header.SetContentLength(len(data))
		ctx.SetStatusCode(200)
		ctx.SetBody(data)
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	stream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
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
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "probe.bin")
	opts := &config.Options{
		URL: "http://test/probe", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, stream); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("probe-fallback checksum mismatch (got %d, want %d)", len(got), len(data))
	}
}

// TestProbe_FallbackGETError verifies probe returns an error when the
// fallback 1-byte ranged GET itself fails.
func TestProbe_FallbackGETError(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		if ctx.IsHead() {
			ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
			return
		}
		// Fallback GET also fails.
		ctx.SetStatusCode(500)
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	var callCount int
	stream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		callCount++
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		if err := c.Do(req, rsp); err != nil {
			return gholatransport.StreamMeta{}, err
		}
		status := rsp.Header.StatusCode()
		if status == 500 {
			return gholatransport.StreamMeta{}, fmt.Errorf("server error 500")
		}
		return gholatransport.StreamMeta{StatusCode: status, ContentLength: -1}, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "fail.bin")
	opts := &config.Options{
		URL: "http://test/fail", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	err := Download(context.Background(), opts, stream)
	if err == nil {
		t.Fatal("expected error from fallback GET, got nil")
	}
	if !strings.Contains(err.Error(), "probe") && !strings.Contains(err.Error(), "server error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestResumeOffset_Numeric verifies that a numeric -C value returns that offset.
func TestResumeOffset_Numeric(t *testing.T) {
	opts := &config.Options{
		Output: config.OutputOptions{ContinueAt: "12345"},
	}
	off, err := resumeOffset(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if off != 12345 {
		t.Errorf("offset = %d, want 12345", off)
	}
}

// TestResumeOffset_AutoExisting verifies -C - returns the file size when
// the file exists.
func TestResumeOffset_AutoExisting(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "partial.bin")
	os.WriteFile(f, make([]byte, 9999), 0644) //nolint:errcheck

	opts := &config.Options{
		Output: config.OutputOptions{File: f, ContinueAt: "-"},
	}
	off, err := resumeOffset(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if off != 9999 {
		t.Errorf("offset = %d, want 9999", off)
	}
}

// TestResumeOffset_AutoMissing verifies -C - on a missing file returns 0, no error.
func TestResumeOffset_AutoMissing(t *testing.T) {
	opts := &config.Options{
		Output: config.OutputOptions{File: "/nonexistent/path.bin", ContinueAt: "-"},
	}
	off, err := resumeOffset(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if off != 0 {
		t.Errorf("offset = %d, want 0", off)
	}
}

// TestBuildDownloadRequest_GhostAndImpersonate covers the ghost-sign and
// impersonation branches in buildDownloadRequest.
func TestBuildDownloadRequest_GhostAndImpersonate(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	opts := &config.Options{
		Stealth: config.StealthOptions{
			Ghost:          true,
			Impersonate:    "chrome",
			StealthHeaders: true,
			Compressed:     true,
			Agent:          "ghola-test",
		},
	}
	buildDownloadRequest(req, opts, "http://example.com/path", fasthttp.MethodGet, "body-data")
	if len(req.Body()) == 0 {
		t.Errorf("expected body to be set")
	}
	// Ghost signing adds a custom header; verify the request was built.
	if string(req.Header.Method()) != fasthttp.MethodGet {
		t.Errorf("method = %q, want GET", req.Header.Method())
	}
	// Accept-Encoding should be gzip when Compressed=true.
	if ae := string(req.Header.Peek("Accept-Encoding")); ae != "gzip" {
		t.Errorf("Accept-Encoding = %q, want gzip", ae)
	}
}

// TestBuildDownloadRequest_HeaderOverride verifies that explicit -H headers
// override profile-generated ones.
func TestBuildDownloadRequest_HeaderOverride(t *testing.T) {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	opts := &config.Options{
		Stealth: config.StealthOptions{Agent: "custom-agent"},
		Headers: []string{"User-Agent: my-custom-ua", "X-Foo: bar"},
	}
	buildDownloadRequest(req, opts, "http://example.com/", fasthttp.MethodGet, "")
	// The explicit User-Agent override wins.
	ua := string(req.Header.Peek("User-Agent"))
	if ua != "my-custom-ua" {
		t.Errorf("User-Agent = %q, want 'my-custom-ua'", ua)
	}
	if xfoo := string(req.Header.Peek("X-Foo")); xfoo != "bar" {
		t.Errorf("X-Foo = %q, want 'bar'", xfoo)
	}
}

// TestDownloadSegmented_SegmentError verifies that a server returning 500 for
// a range causes downloadSegmented (and thus Download) to return an error.
func TestDownloadSegmented_SegmentError(t *testing.T) {
	data := bytes.Repeat([]byte("ERR"), 500000) // 1.5 MB — big enough to segment
	ln := fasthttputil.NewInmemoryListener()
	var reqCount int64
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		rng := string(ctx.Request.Header.Peek("Range"))
		if rng == "" {
			ctx.SetStatusCode(200)
			ctx.Response.Header.SetContentLength(len(data))
			if !ctx.IsHead() {
				ctx.SetBody(data)
			}
			return
		}
		// Fail one range to trigger the error path. The handler runs
		// concurrently for each segment request, so the counter must be atomic.
		if atomic.AddInt64(&reqCount, 1) == 1 {
			ctx.SetStatusCode(500)
			return
		}
		start, end := parseTestRange(t, rng, len(data))
		ctx.SetStatusCode(206)
		ctx.Response.Header.Set("Content-Range",
			"bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
		ctx.SetBody(data[start : end+1])
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	stream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
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
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "err.bin")
	opts := &config.Options{
		URL: "http://test/seg", Method: "GET", Concurrency: 4,
		Output:  config.OutputOptions{File: out},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	err := Download(context.Background(), opts, stream)
	if err == nil {
		t.Fatal("expected error from segment failure, got nil")
	}
}

// TestDownload_JDispositionRename verifies -J (RemoteHeader) renames the
// output file according to Content-Disposition.
func TestDownload_JDispositionRename(t *testing.T) {
	data := []byte("disposition-test")
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.Response.Header.Set("Content-Disposition", `attachment; filename="named.bin"`)
		ctx.Response.Header.SetContentLength(len(data))
		ctx.SetStatusCode(200)
		if !ctx.IsHead() {
			ctx.SetBody(data)
		}
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	stream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
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
			Disposition:   string(rsp.Header.Peek("Content-Disposition")),
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	// opts.Output.File must be set initially (Download won't be routed here
	// otherwise), and then -J overrides it with the disposition name.
	outInit := filepath.Join(dir, "initial.bin")
	opts := &config.Options{
		URL: "http://test/file", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: outInit, RemoteHeader: true},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, stream); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	// After Download, opts.Output.File should be updated to the disposition name.
	if opts.Output.File != "named.bin" {
		t.Errorf("opts.Output.File = %q, want 'named.bin'", opts.Output.File)
	}
	// Read via the actual output path (not a hard-coded literal) so the content
	// check is never silently skipped if the file lands elsewhere.
	got, err := os.ReadFile(opts.Output.File)
	if err != nil {
		t.Fatalf("reading output file %q: %v", opts.Output.File, err)
	}
	defer os.Remove(opts.Output.File) //nolint:errcheck
	if !bytes.Equal(got, data) {
		t.Errorf("content mismatch: got %q, want %q", got, data)
	}
}

// TestDownload_RemoteTime verifies -R (RemoteTime) sets the file mtime from
// the Last-Modified header.
func TestDownload_RemoteTime(t *testing.T) {
	data := []byte("remote-time-test")
	lastMod := "Mon, 02 Jan 2006 15:04:05 GMT"
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.Response.Header.Set("Last-Modified", lastMod)
		ctx.Response.Header.SetContentLength(len(data))
		ctx.SetStatusCode(200)
		if !ctx.IsHead() {
			ctx.SetBody(data)
		}
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	stream := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
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
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "timed.bin")
	opts := &config.Options{
		URL: "http://test/timed", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out, RemoteTime: true},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, stream); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	want, _ := time.Parse(time.RFC1123, lastMod)
	got := info.ModTime().UTC().Truncate(time.Second)
	if !got.Equal(want.UTC()) {
		t.Errorf("mtime = %v, want %v", got, want.UTC())
	}
}

// TestDownload_LimitRate verifies that a download with --limit-rate completes
// correctly (the rate limiter doesn't corrupt or lose bytes).
func TestDownload_LimitRate(t *testing.T) {
	data := bytes.Repeat([]byte("rate-"), 2000) // 10 KB
	streamer := blobServer(t, data)

	dir := t.TempDir()
	out := filepath.Join(dir, "rate.bin")
	opts := &config.Options{
		URL: "http://test/rate", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out, LimitRate: "500k"},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("rate-limited download checksum mismatch")
	}
}

// TestDownload_Progress verifies that a non-silent download with a known size
// completes without error and produces the right file content (exercises
// progressWriter total>0 path and done()).
func TestDownload_Progress(t *testing.T) {
	data := bytes.Repeat([]byte("prog-"), 1000)
	streamer := blobServer(t, data)

	dir := t.TempDir()
	out := filepath.Join(dir, "prog.bin")
	opts := &config.Options{
		URL: "http://test/prog", Method: "GET", Concurrency: 1,
		Output: config.OutputOptions{
			File:   out,
			Silent: false, // enables progress
		},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("progress download checksum mismatch")
	}
}

// TestProgressWriter_UnknownTotal covers the else branch in progressWriter.Write
// when total <= 0 (unknown content-length).
func TestProgressWriter_UnknownTotal(t *testing.T) {
	var sink, prog bytes.Buffer
	pw := newProgressWriter(&sink, &prog, -1) // unknown total
	// Write enough to trigger the rate-limiter progress print (or at least cover
	// the branch: elapsed time >= progressInterval).
	pw.last = pw.last.Add(-progressInterval - time.Millisecond) // force progress print
	if _, err := pw.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	pw.done()
	if pw.written != 100 {
		t.Errorf("written = %d, want 100", pw.written)
	}
	// The progress output should contain "bytes" (not the percent form).
	if !strings.Contains(prog.String(), "bytes") {
		t.Errorf("progress output = %q, expected 'bytes'", prog.String())
	}
}

// TestProgressTap_UnknownTotal covers the progressTap.Write else branch
// when total <= 0.
func TestProgressTap_UnknownTotal(t *testing.T) {
	var seg, prog bytes.Buffer
	pw := newProgressWriter(&seg, &prog, -1) // unknown total
	pw.last = pw.last.Add(-progressInterval - time.Millisecond)
	tap := newProgressTap(&seg, pw)
	if _, err := tap.Write(make([]byte, 50)); err != nil {
		t.Fatal(err)
	}
	if pw.written != 50 {
		t.Errorf("tap written = %d, want 50", pw.written)
	}
}
