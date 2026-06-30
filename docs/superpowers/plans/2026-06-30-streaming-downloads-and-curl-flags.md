# Streaming Downloads + curl-Compatible Flags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream large HTTP downloads to disk with bounded memory (fixing the OOM at `-n 1` and the 49 GB blow-up at `-n 8`), make `-n` accelerate downloads via HTTP Range segmentation, add resume, and add the missing curl-compatible flags.

**Architecture:** Additive streaming path that coexists with the existing buffered pipeline. A new `Streamer` func seam (mirroring the existing `Doer`) and a `Transport.Stream` method write response bodies directly to an `io.Writer`. A `client.Download` orchestrator probes the target, then either segments the file across N Range requests (writing at offsets) or streams a single connection. The buffered pipeline (`FetchURL`, `ProcessResponse`) is untouched, so `--jq`/`--snoop`/`--har`/stdout and the `bridge` contract keep working.

**Tech Stack:** Go 1.26, `github.com/valyala/fasthttp`, `github.com/bogdanfinn/tls-client` + `fhttp`, `github.com/spf13/pflag`. Standard library only for streaming, rate limiting (token bucket), and concurrency (`sync` + `context`) — no new module dependencies.

## Global Constraints

- Module path: `github.com/robot-accomplice/ghola`. Go floor: `go 1.26.4` (verbatim from `go.mod`).
- **No new module dependencies.** Use stdlib for rate limiting and concurrency. (gzip via `compress/gzip`; brotli is out of scope.)
- **CI coverage gate:** each package ≥80% line coverage and no regression below recorded baselines. New code must carry tests that hold this.
- **Buffered pipeline is read-only for this work.** Do not change `FetchURL`, `ProcessResponse`, `RunConcurrent` semantics, or the `bridge.Request`/`bridge.Response` structs (consumed by scope, unversioned).
- **Flag-letter divergences stay:** `-c`=`--chain`, `-G`=`--ghost`, `-b`=`--backoff`. Every new short flag below (`-C`, `-O`, `-J`, `-R`, `-k`, `-F`) uses a letter currently free in ghola. Do not reassign existing letters.
- Defaults live in `internal/config/defaults.go` as named constants — no magic literals (project rule).
- Branch: `feat/streaming-downloads` (already created). Commit per task.

---

## File Structure

- `internal/transport/transport.go` — **modify**: add `StreamMeta` type and `Stream` method to the `Transport` interface and both implementations; add `tls.Config` construction (Task 1, Task 8).
- `internal/client/stream.go` — **create**: `Streamer` func type, `DefaultStreamer`, `StreamMeta` re-export glue (Task 1).
- `internal/client/download.go` — **create**: `Download` orchestrator (probe, single-stream, segmented, resume) (Tasks 2–5).
- `internal/client/ratelimit.go` — **create**: token-bucket `io.Writer` + throttled progress writer (Task 7).
- `internal/config/config.go` — **modify**: new flags + `Options` fields + validation (Tasks 2,3,5,6,7,8,9,10).
- `internal/config/defaults.go` — **modify**: new default constants (Tasks 2,4,7).
- `cmd/ghola/main.go` — **modify**: route file-destined GETs to `client.Download` (Task 2).
- Tests colocated as `*_test.go` in each package.

---

## Task 1: Streaming transport + `Streamer` seam

**Files:**
- Modify: `internal/transport/transport.go`
- Create: `internal/client/stream.go`
- Test: `internal/transport/transport_test.go`, `internal/client/stream_test.go`

**Interfaces:**
- Produces:
  - `transport.StreamMeta{ StatusCode int; ContentLength int64; AcceptRanges bool; LastModified string; Disposition string }`
  - `transport.Transport.Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error)`
  - `client.Streamer` = `func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (transport.StreamMeta, error)`
  - `client.DefaultStreamer` with that signature.

- [ ] **Step 1: Write the failing test for `simpleTransport.Stream`**

Add to `internal/transport/transport_test.go`:

```go
func TestSimpleTransport_StreamWritesBodyToSink(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		ctx.SetBodyString("streamed-body")
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	tr := &simpleTransport{
		client: &fasthttp.Client{
			StreamResponseBody: true,
			Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
		},
		name: "fasthttp",
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://test/")
	req.Header.SetMethod("GET")

	var buf bytes.Buffer
	meta, err := tr.Stream(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}
	if meta.StatusCode != 200 {
		t.Errorf("status = %d, want 200", meta.StatusCode)
	}
	if !meta.AcceptRanges {
		t.Error("AcceptRanges = false, want true")
	}
	if buf.String() != "streamed-body" {
		t.Errorf("sink = %q, want streamed-body", buf.String())
	}
}
```

Ensure the test file imports `bytes`, `context`, `net`, `github.com/valyala/fasthttp`, `github.com/valyala/fasthttp/fasthttputil`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/ -run TestSimpleTransport_Stream -v`
Expected: FAIL — `tr.Stream undefined (type *simpleTransport has no field or method Stream)`.

- [ ] **Step 3: Add `StreamMeta` and `Stream` to the interface and both transports**

In `internal/transport/transport.go`, add the type and extend the interface:

```go
// StreamMeta carries the response status line and the headers the download
// orchestrator needs, without materializing the body.
type StreamMeta struct {
	StatusCode    int
	ContentLength int64 // -1 when unknown
	AcceptRanges  bool
	LastModified  string
	Disposition   string // Content-Disposition
}

// Transport performs a request using the selected runtime backend.
type Transport interface {
	Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error
	// Stream sends req and copies the response body to sink without buffering
	// it in memory. It does NOT follow redirects; callers handle 3xx via the
	// returned StreamMeta. The caller owns sink.
	Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error)
	Name() string
}
```

Add a shared header-extraction helper:

```go
func metaFromResponseHeader(h *fasthttp.ResponseHeader) StreamMeta {
	cl := int64(-1)
	if n := h.ContentLength(); n >= 0 {
		cl = int64(n)
	}
	return StreamMeta{
		StatusCode:    h.StatusCode(),
		ContentLength: cl,
		AcceptRanges:  bytes.EqualFold(h.Peek("Accept-Ranges"), []byte("bytes")),
		LastModified:  string(h.Peek("Last-Modified")),
		Disposition:   string(h.Peek("Content-Disposition")),
	}
}
```

Implement for `simpleTransport` (fasthttp must be told to stream; set it on the client in `New`, see Step 5):

```go
func (t *simpleTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	rsp.StreamBody = true

	var err error
	if deadline, ok := ctx.Deadline(); ok {
		err = t.client.DoDeadline(req, rsp, deadline)
	} else {
		err = t.client.Do(req, rsp)
	}
	if err != nil {
		return StreamMeta{}, err
	}
	meta := metaFromResponseHeader(&rsp.Header)
	if err := rsp.BodyWriteTo(sink); err != nil {
		return meta, err
	}
	return meta, nil
}
```

Implement for `tlsClientTransport` (replace `io.ReadAll` with a streaming copy; the existing `Do` keeps using `io.ReadAll`):

```go
func (t *tlsClientTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	httpReq, err := fhttp.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), bytes.NewReader(req.Body()))
	if err != nil {
		return StreamMeta{}, err
	}
	httpReq.Header = make(fhttp.Header)
	for key, value := range req.Header.All() {
		httpReq.Header.Set(string(key), string(value))
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return StreamMeta{}, err
	}
	defer httpResp.Body.Close()

	meta := StreamMeta{
		StatusCode:    httpResp.StatusCode,
		ContentLength: httpResp.ContentLength, // -1 when unknown, per net/http
		AcceptRanges:  strings.EqualFold(httpResp.Header.Get("Accept-Ranges"), "bytes"),
		LastModified:  httpResp.Header.Get("Last-Modified"),
		Disposition:   httpResp.Header.Get("Content-Disposition"),
	}
	if _, err := io.Copy(sink, httpResp.Body); err != nil {
		return meta, err
	}
	return meta, nil
}
```

- [ ] **Step 4: Run the transport test to verify it passes**

Run: `go test ./internal/transport/ -run TestSimpleTransport_Stream -v`
Expected: PASS.

- [ ] **Step 5: Make `simpleTransport`'s client stream-capable**

In `transport.New`, set `StreamResponseBody` on the constructed `fasthttp.Client` so `Stream` works in production:

```go
	client := &fasthttp.Client{
		ReadBufferSize:     opts.Resilience.BufferSize,
		StreamResponseBody: true,
	}
```

(Streaming the body has no effect on the buffered `Do` path — fasthttp still exposes `rsp.Body()` for non-streamed responses; `Do` callers read `rsp.Body()` after the call as before. Verify the existing transport tests still pass in Step 8.)

- [ ] **Step 6: Write the failing test for `DefaultStreamer`**

Create `internal/client/stream_test.go`:

```go
package client

import (
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// newStreamTestServer returns an in-mem listener and a Streamer wired to it.
func newStreamTestServer(handler fasthttp.RequestHandler) (*fasthttputil.InmemoryListener, Streamer) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: handler, StreamRequestBody: false}
	go srv.Serve(ln) //nolint:errcheck
	c := &fasthttp.Client{
		StreamResponseBody: true,
		Dial:               func(addr string) (net.Conn, error) { return ln.Dial() },
	}
	streamer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
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
	}
	return ln, streamer
}

func TestStreamer_BasicGET(t *testing.T) {
	ln, streamer := newStreamTestServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString("payload")
	})
	defer ln.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://test/")
	req.Header.SetMethod("GET")

	var buf bytes.Buffer
	meta, err := streamer(context.Background(), &config.Options{}, req, &buf)
	if err != nil {
		t.Fatalf("streamer error: %v", err)
	}
	if meta.StatusCode != 200 || buf.String() != "payload" {
		t.Fatalf("got status=%d body=%q", meta.StatusCode, buf.String())
	}
}
```

Add `"io"` to the imports.

- [ ] **Step 7: Add the `Streamer` type and `DefaultStreamer`**

Create `internal/client/stream.go`:

```go
package client

import (
	"context"
	"io"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Streamer abstracts a single streaming round-trip so tests can inject a
// transport backed by an in-memory listener. It mirrors Doer for the
// streaming download path. The body is written to sink; the returned
// StreamMeta carries status + headers. Streamer does NOT follow redirects.
type Streamer func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error)

// DefaultStreamer builds the production transport and streams through it.
func DefaultStreamer(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
	tr, err := gholatransport.New(opts, "")
	if err != nil {
		return gholatransport.StreamMeta{}, err
	}
	return tr.Stream(ctx, req, sink)
}
```

- [ ] **Step 8: Run all tests in both packages**

Run: `go test ./internal/transport/ ./internal/client/ -v`
Expected: PASS (including the pre-existing transport/client tests — confirms the buffered path is unaffected).

- [ ] **Step 9: Commit**

```bash
git add internal/transport/transport.go internal/transport/transport_test.go internal/client/stream.go internal/client/stream_test.go
git commit -m "feat(transport): add streaming Stream method and Streamer seam"
```

---

## Task 2: Single-stream download orchestrator + routing

**Files:**
- Create: `internal/client/download.go`
- Modify: `internal/config/config.go`, `internal/config/defaults.go`, `cmd/ghola/main.go`
- Test: `internal/client/download_test.go`

**Interfaces:**
- Consumes: `client.Streamer`, `transport.StreamMeta` (Task 1); existing `handleRedirect`, `isRedirectStatus`, `resolveLocation` (in `client.go`).
- Produces:
  - `client.Download(ctx context.Context, opts *config.Options, stream Streamer) error`
  - `config.ShouldStream(opts *Options) bool` — the routing predicate.
  - `config.DefaultCopyBufferSize` constant.

- [ ] **Step 1: Write the failing test for a single-stream download to a file**

Create `internal/client/download_test.go`:

```go
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// blobServer serves `data`, honoring Range requests and advertising
// Accept-Ranges: bytes. Returns a Streamer wired to it.
func blobServer(t *testing.T, data []byte) Streamer {
	t.Helper()
	ln := fasthttputil.NewInmemoryListener()
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
		start, end := parseTestRange(t, rng, len(data)) // bytes=start-end inclusive
		ctx.SetStatusCode(206)
		ctx.Response.Header.Set("Content-Range",
			"bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
		ctx.SetBody(data[start : end+1])
	}}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { ln.Close() })
	c := &fasthttp.Client{StreamResponseBody: true, Dial: func(addr string) (net.Conn, error) { return ln.Dial() }}
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
	}
}

func TestDownload_SingleStreamWritesFullFile(t *testing.T) {
	data := bytes.Repeat([]byte("ghola-"), 100000) // 600 KB
	streamer := blobServer(t, data)

	dir := t.TempDir()
	out := filepath.Join(dir, "blob.bin")
	opts := &config.Options{
		URL:         "http://test/blob.bin",
		Method:      "GET",
		Concurrency: 1,
		Output:      config.OutputOptions{File: out},
		Stealth:     config.StealthOptions{Agent: "ghola-test"},
	}

	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("downloaded file checksum mismatch (got %d bytes, want %d)", len(got), len(data))
	}
}
```

Add the small test helpers at the bottom of the file:

```go
func itoa(n int) string { return strconv.Itoa(n) }

func parseTestRange(t *testing.T, rng string, size int) (int, int) {
	t.Helper()
	// rng like "bytes=START-END" or "bytes=START-"
	spec := strings.TrimPrefix(rng, "bytes=")
	parts := strings.SplitN(spec, "-", 2)
	start, _ := strconv.Atoi(parts[0])
	end := size - 1
	if len(parts) == 2 && parts[1] != "" {
		end, _ = strconv.Atoi(parts[1])
	}
	if end > size-1 {
		end = size - 1
	}
	return start, end
}
```

Imports needed: add `"io"`, `"strconv"`, `"strings"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestDownload_SingleStream -v`
Expected: FAIL — `undefined: Download`.

- [ ] **Step 3: Implement the single-stream orchestrator**

Create `internal/client/download.go`:

```go
// Package client — download.go implements the streaming download path used
// for file-destined GET requests. It writes bodies directly to disk with
// bounded memory, in contrast to the buffered FetchURL path.
package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Download fetches opts.URL and streams it to opts.Output.File with bounded
// memory. Routing (when to call this vs. FetchURL) is decided by
// config.ShouldStream in the CLI entry point.
func Download(ctx context.Context, opts *config.Options, stream Streamer) error {
	if stream == nil {
		stream = DefaultStreamer
	}
	finalURL, err := resolveTarget(ctx, opts, stream)
	if err != nil {
		return err
	}
	return downloadSingle(ctx, opts, stream, finalURL)
}

// resolveTarget follows redirects with a HEAD request and returns the final
// URL. It does not require the server to support HEAD beyond returning a
// status; the body (if any) is discarded.
func resolveTarget(ctx context.Context, opts *config.Options, stream Streamer) (string, error) {
	target := opts.URL
	for hops := 0; hops <= opts.Resilience.MaxRedirs; hops++ {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, target, fasthttp.MethodHead, "")
		meta, err := stream(ctx, opts, req, io.Discard)
		fasthttp.ReleaseRequest(req)
		if err != nil {
			return "", err
		}
		if !isRedirectStatus(meta.StatusCode) {
			return target, nil
		}
		// HEAD redirect: re-fetch with GET semantics handled in downloadSingle.
		// Location is not in StreamMeta; fall through to a GET-based resolve.
		return resolveTargetViaGet(ctx, opts, stream, target)
	}
	return target, nil
}

// resolveTargetViaGet resolves redirects when HEAD returns 3xx by issuing a
// ranged GET (bytes=0-0) and reading Location from the response.
func resolveTargetViaGet(ctx context.Context, opts *config.Options, stream Streamer, start string) (string, error) {
	target := start
	for hops := 0; hops <= opts.Resilience.MaxRedirs; hops++ {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, target, fasthttp.MethodGet, "")
		req.Header.Set("Range", "bytes=0-0")
		loc, status, err := streamLocation(ctx, opts, stream, req)
		fasthttp.ReleaseRequest(req)
		if err != nil {
			return "", err
		}
		if !isRedirectStatus(status) {
			return target, nil
		}
		resolved, ok := resolveLocation(target, loc)
		if !ok {
			return "", fmt.Errorf("download: bad redirect Location %q", loc)
		}
		target = resolved
	}
	return "", fmt.Errorf("download: too many redirects (>%d)", opts.Resilience.MaxRedirs)
}

// streamLocation runs a request, discards the body, and returns the Location
// header + status. It needs the raw header, so it uses a buffered transport
// call via DefaultDoer-style access is avoided; instead it relies on the
// Streamer plus a header sink. Since StreamMeta omits Location, we capture it
// through a one-shot buffered fetch.
func streamLocation(ctx context.Context, opts *config.Options, stream Streamer, req *fasthttp.Request) (string, int, error) {
	// Use FetchURL's transport directly for redirect resolution: a HEAD/probe
	// body is tiny, so a buffered fetch is acceptable here.
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	tr, err := gholatransport.New(opts, "")
	if err != nil {
		return "", 0, err
	}
	if err := tr.Do(ctx, req, rsp); err != nil {
		return "", 0, err
	}
	return string(rsp.Header.Peek("Location")), rsp.Header.StatusCode(), nil
}

func downloadSingle(ctx context.Context, opts *config.Options, stream Streamer, url string) error {
	f, err := os.OpenFile(opts.Output.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, opts.Data)

	meta, err := stream(ctx, opts, req, f)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if meta.StatusCode < 200 || meta.StatusCode >= 300 {
		return fmt.Errorf("download: non-2xx status %d", meta.StatusCode)
	}
	return nil
}

// buildDownloadRequest sets URI, method, body, headers, and the active
// browser profile / ghost / auth identity, mirroring prepareRequest but for
// the streaming path. It reuses the profile builder for header realism.
func buildDownloadRequest(req *fasthttp.Request, opts *config.Options, url, method, data string) {
	req.SetRequestURI(url)
	req.Header.SetMethod(method)
	if data != "" {
		req.SetBodyString(data)
	}

	var active profile.BrowserProfile
	if opts.Stealth.Impersonate != "" || opts.Stealth.StealthHeaders {
		if resolved, err := profile.Resolve(opts.Stealth.Impersonate); err == nil {
			active = resolved
		}
	}
	overrides := parseHeaderOverrides(opts.Headers)
	for _, generated := range profile.BuildHeaders(active, profile.HeaderOptions{
		Method:         method,
		TargetURL:      url,
		RefererMode:    opts.Stealth.Referer,
		ExplicitUA:     resolveUserAgent(opts, active),
		ExplicitLang:   opts.Stealth.AcceptLanguage,
		StealthHeaders: opts.Stealth.StealthHeaders,
	}) {
		if _, ok := overrides[strings.ToLower(generated.Key)]; ok {
			continue
		}
		req.Header.Set(generated.Key, generated.Value)
	}
	for _, h := range opts.Headers {
		if k, v, ok := parseHeader(h); ok {
			req.Header.Set(k, v)
		}
	}
	if _, ok := overrides["user-agent"]; !ok {
		req.Header.Set("User-Agent", resolveUserAgent(opts, active))
	}
	if opts.Stealth.Ghost {
		applyGhostSign(req, url)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/client/ -run TestDownload_SingleStream -v`
Expected: PASS.

- [ ] **Step 5: Add the routing predicate and constant**

In `internal/config/defaults.go` add:

```go
	// DefaultCopyBufferSize is the io.Copy chunk size for streaming downloads.
	DefaultCopyBufferSize = 256 * 1024
```

In `internal/config/config.go` add (after `inferWgetFilename`):

```go
// ShouldStream reports whether a request should take the streaming download
// path (bounded memory, written to a file) instead of the buffered pipeline.
// The buffered path is required when the whole body must be in memory: JSON
// extraction (--jq), snoop, and HAR export.
func ShouldStream(opts *Options) bool {
	if opts.Output.File == "" {
		return false
	}
	if opts.Output.Snoop || opts.Output.JQ != "" || opts.Output.HAR != "" {
		return false
	}
	return opts.Method == fasthttp.MethodGet || opts.Method == "GET"
}
```

- [ ] **Step 6: Write the failing test for routing**

Add to `internal/config/config_test.go`:

```go
func TestShouldStream(t *testing.T) {
	cases := []struct {
		name string
		opts *Options
		want bool
	}{
		{"file get", &Options{Method: "GET", Output: OutputOptions{File: "x"}}, true},
		{"stdout", &Options{Method: "GET"}, false},
		{"jq forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", JQ: ".a"}}, false},
		{"snoop forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", Snoop: true}}, false},
		{"har forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", HAR: "h.har"}}, false},
		{"post not streamed", &Options{Method: "POST", Output: OutputOptions{File: "x"}}, false},
	}
	for _, tc := range cases {
		if got := ShouldStream(tc.opts); got != tc.want {
			t.Errorf("%s: ShouldStream = %v, want %v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 7: Run the config test**

Run: `go test ./internal/config/ -run TestShouldStream -v`
Expected: PASS.

- [ ] **Step 8: Wire routing into the CLI entry point**

In `cmd/ghola/main.go`, before the `opts.Concurrency > 1` block, add the streaming branch:

```go
	if config.ShouldStream(opts) {
		if err := client.Download(ctx, opts, nil); err != nil {
			if !opts.Output.Silent {
				fmt.Fprintf(os.Stderr, "Failed: %s\n", err)
			}
			return config.WriteFileFailed.Int()
		}
		return config.NoError.Int()
	}
```

Place it after the `opts.Data`/stdin handling and before the `opts.Concurrency > 1` block so file-destined GETs always stream (segmentation is added in Task 4). The existing `RunConcurrent` and `FetchURL` branches remain for the non-streaming cases.

- [ ] **Step 9: Run the full suite + build**

Run: `go build ./... && go test ./... `
Expected: build OK, all tests PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/client/download.go internal/client/download_test.go internal/config/config.go internal/config/config_test.go internal/config/defaults.go cmd/ghola/main.go
git commit -m "feat(client): stream file-destined GET downloads to disk (fixes OOM at -n 1)"
```

---

## Task 3: Range splitting helper

**Files:**
- Modify: `internal/client/download.go`
- Test: `internal/client/download_test.go`

**Interfaces:**
- Produces: `splitRanges(size int64, parts int) []byteRange` where `byteRange{ start, end int64 }` (end inclusive).

- [ ] **Step 1: Write the failing test**

Add to `internal/client/download_test.go`:

```go
func TestSplitRanges(t *testing.T) {
	cases := []struct {
		size  int64
		parts int
		want  []byteRange
	}{
		{10, 1, []byteRange{{0, 9}}},
		{10, 2, []byteRange{{0, 4}, {5, 9}}},
		{10, 3, []byteRange{{0, 3}, {4, 7}, {8, 9}}}, // remainder on last
		{2, 5, []byteRange{{0, 0}, {1, 1}}},          // more parts than bytes -> clamp
		{0, 3, nil},                                  // empty
	}
	for _, tc := range cases {
		got := splitRanges(tc.size, tc.parts)
		if len(got) != len(tc.want) {
			t.Fatalf("size=%d parts=%d: got %v, want %v", tc.size, tc.parts, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("size=%d parts=%d idx %d: got %v, want %v", tc.size, tc.parts, i, got[i], tc.want[i])
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestSplitRanges -v`
Expected: FAIL — `undefined: splitRanges` / `undefined: byteRange`.

- [ ] **Step 3: Implement `splitRanges`**

Add to `internal/client/download.go`:

```go
// byteRange is an inclusive [start, end] byte range for a Range request.
type byteRange struct {
	start int64
	end   int64
}

// splitRanges divides size bytes into at most `parts` contiguous inclusive
// ranges. When parts exceeds size, it clamps to one range per byte. Returns
// nil for size <= 0.
func splitRanges(size int64, parts int) []byteRange {
	if size <= 0 || parts <= 0 {
		return nil
	}
	if int64(parts) > size {
		parts = int(size)
	}
	chunk := size / int64(parts)
	ranges := make([]byteRange, 0, parts)
	var start int64
	for i := 0; i < parts; i++ {
		end := start + chunk - 1
		if i == parts-1 {
			end = size - 1 // remainder on the last segment
		}
		ranges = append(ranges, byteRange{start: start, end: end})
		start = end + 1
	}
	return ranges
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/client/ -run TestSplitRanges -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/client/download.go internal/client/download_test.go
git commit -m "feat(client): add byte-range splitting for segmented downloads"
```

---

## Task 4: Segmented parallel download

**Files:**
- Modify: `internal/client/download.go`, `internal/config/defaults.go`
- Test: `internal/client/download_test.go`

**Interfaces:**
- Consumes: `splitRanges`, `byteRange` (Task 3); `Streamer`, `StreamMeta` (Task 1).
- Produces: `downloadSegmented(ctx, opts, stream, url string, size int64) error`; `config.DefaultSegmentMinBytes` constant. `Download` now dispatches single vs. segmented.

- [ ] **Step 1: Write the failing test (segmented reconstructs the file)**

Add to `internal/client/download_test.go`:

```go
func TestDownload_SegmentedReconstructsFile(t *testing.T) {
	data := bytes.Repeat([]byte("SEG"), 500000) // 1.5 MB
	streamer := blobServer(t, data)

	dir := t.TempDir()
	out := filepath.Join(dir, "seg.bin")
	opts := &config.Options{
		URL:         "http://test/seg.bin",
		Method:      "GET",
		Concurrency: 8,
		Output:      config.OutputOptions{File: out},
		Stealth:     config.StealthOptions{Agent: "ghola-test"},
	}

	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("segmented checksum mismatch (got %d bytes, want %d)", len(got), len(data))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestDownload_Segmented -v`
Expected: FAIL — the file is written as a single stream (Task 2 path), but this asserts byte-equality which will currently pass via single-stream. To make the test meaningful, force segmentation: temporarily it passes via single-stream. **Instead**, assert segmentation actually ran by counting Range requests. Replace the test body's tail with a request-counting server:

Add this helper and rewrite the test to use it:

```go
// countingBlobServer is blobServer plus an atomic counter of Range requests.
func countingBlobServer(t *testing.T, data []byte, rangeHits *int64) Streamer {
	t.Helper()
	ln := fasthttputil.NewInmemoryListener()
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
		atomic.AddInt64(rangeHits, 1)
		start, end := parseTestRange(t, rng, len(data))
		ctx.SetStatusCode(206)
		ctx.Response.Header.Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
		ctx.SetBody(data[start : end+1])
	}}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { ln.Close() })
	c := &fasthttp.Client{StreamResponseBody: true, Dial: func(addr string) (net.Conn, error) { return ln.Dial() }}
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
		}
		if err := rsp.BodyWriteTo(sink); err != nil {
			return meta, err
		}
		return meta, nil
	}
}
```

Rewrite `TestDownload_SegmentedReconstructsFile` to assert both checksum and that multiple Range requests occurred:

```go
func TestDownload_SegmentedReconstructsFile(t *testing.T) {
	data := bytes.Repeat([]byte("SEG"), 500000) // 1.5 MB
	var rangeHits int64
	streamer := countingBlobServer(t, data, &rangeHits)

	dir := t.TempDir()
	out := filepath.Join(dir, "seg.bin")
	opts := &config.Options{
		URL: "http://test/seg.bin", Method: "GET", Concurrency: 8,
		Output:  config.OutputOptions{File: out},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("segmented checksum mismatch (got %d, want %d)", len(got), len(data))
	}
	if hits := atomic.LoadInt64(&rangeHits); hits < 2 {
		t.Fatalf("expected multiple Range requests, got %d", hits)
	}
}
```

Add `"sync/atomic"` to imports.

- [ ] **Step 3: Implement segmentation dispatch + worker**

In `internal/config/defaults.go` add:

```go
	// DefaultSegmentMinBytes is the minimum Content-Length for which a
	// multi-connection (-n>1) download is split into Range segments.
	DefaultSegmentMinBytes = 1 << 20 // 1 MiB
```

In `internal/client/download.go`, change `Download` to probe size and dispatch, and add the segmented worker. Replace the body of `Download`:

```go
func Download(ctx context.Context, opts *config.Options, stream Streamer) error {
	if stream == nil {
		stream = DefaultStreamer
	}
	finalURL, meta, err := probe(ctx, opts, stream)
	if err != nil {
		return err
	}
	if canSegment(opts, meta) {
		return downloadSegmented(ctx, opts, stream, finalURL, meta.ContentLength)
	}
	return downloadSingle(ctx, opts, stream, finalURL)
}

// probe resolves redirects and returns the final URL and its StreamMeta
// (Content-Length, Accept-Ranges) via a HEAD request, falling back to a
// ranged GET when HEAD is unsupported.
func probe(ctx context.Context, opts *config.Options, stream Streamer) (string, gholatransport.StreamMeta, error) {
	finalURL, err := resolveTarget(ctx, opts, stream)
	if err != nil {
		return "", gholatransport.StreamMeta{}, err
	}
	req := fasthttp.AcquireRequest()
	buildDownloadRequest(req, opts, finalURL, fasthttp.MethodHead, "")
	meta, err := stream(ctx, opts, req, io.Discard)
	fasthttp.ReleaseRequest(req)
	if err != nil {
		return "", gholatransport.StreamMeta{}, err
	}
	if meta.ContentLength < 0 || (meta.StatusCode != 200 && meta.StatusCode != 0) {
		// HEAD unsupported or no length: probe with a 1-byte ranged GET.
		req2 := fasthttp.AcquireRequest()
		buildDownloadRequest(req2, opts, finalURL, fasthttp.MethodGet, "")
		req2.Header.Set("Range", "bytes=0-0")
		m2, err := stream(ctx, opts, req2, io.Discard)
		fasthttp.ReleaseRequest(req2)
		if err == nil && m2.AcceptRanges {
			meta = m2
		}
	}
	return finalURL, meta, nil
}

// canSegment reports whether a segmented (parallel Range) download applies.
func canSegment(opts *config.Options, meta gholatransport.StreamMeta) bool {
	return opts.Concurrency > 1 &&
		opts.Output.File != "" &&
		meta.AcceptRanges &&
		meta.ContentLength >= config.DefaultSegmentMinBytes &&
		!opts.Stealth.Compressed && // set in Task 9; field exists by then
		opts.Output.ContinueAt == "" // set in Task 5; resume forces single-stream
}

func downloadSegmented(ctx context.Context, opts *config.Options, stream Streamer, url string, size int64) error {
	f, err := os.OpenFile(opts.Output.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("preallocate output file: %w", err)
	}

	ranges := splitRanges(size, opts.Concurrency)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(ranges))
	var wg sync.WaitGroup
	for _, r := range ranges {
		wg.Add(1)
		go func(r byteRange) {
			defer wg.Done()
			errs <- fetchSegment(ctx, opts, stream, url, f, r)
		}(r)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		if e != nil {
			cancel()
			return fmt.Errorf("download segment: %w", e)
		}
	}
	return nil
}

// fetchSegment fetches one byte range and writes it at its offset in f.
func fetchSegment(ctx context.Context, opts *config.Options, stream Streamer, url string, f *os.File, r byteRange) error {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, "")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.start, r.end))

	w := io.NewOffsetWriter(f, r.start)
	meta, err := stream(ctx, opts, req, w)
	if err != nil {
		return err
	}
	if meta.StatusCode != fasthttp.StatusPartialContent && meta.StatusCode != 200 {
		return fmt.Errorf("unexpected status %d for range %d-%d", meta.StatusCode, r.start, r.end)
	}
	return nil
}
```

Add `"sync"` to the `download.go` imports. **Note:** `canSegment` references `opts.Stealth.Compressed` and `opts.Output.ContinueAt`, which are added in Tasks 9 and 5. To keep this task compiling on its own, add those two fields now as no-ops:
- In `internal/config/config.go`, add `ContinueAt string` to `OutputOptions` and `Compressed bool` to `StealthOptions` (flags wired later). This is the one forward-declaration in the plan; the fields are inert until their flags exist.

- [ ] **Step 4: Run the segmented test**

Run: `go test ./internal/client/ -run TestDownload_Segmented -v`
Expected: PASS (checksum matches and ≥2 Range requests recorded).

- [ ] **Step 5: Run the full client suite (single-stream still green)**

Run: `go test ./internal/client/ -v`
Expected: PASS — both `TestDownload_SingleStreamWritesFullFile` (n=1) and the segmented test pass.

- [ ] **Step 6: Commit**

```bash
git add internal/client/download.go internal/client/download_test.go internal/config/config.go internal/config/defaults.go
git commit -m "feat(client): segmented parallel range download for -n>1 (fixes -n OOM)"
```

---

## Task 5: Resume (`-C`/`--continue-at`) + `--range`

**Files:**
- Modify: `internal/config/config.go`, `internal/client/download.go`
- Test: `internal/client/download_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `downloadSingle` (Task 2), `buildDownloadRequest`.
- Produces: `Options.Output.ContinueAt` (declared in Task 4, flag wired here), `Options.Output.Range`; resume logic in `downloadSingle`.

- [ ] **Step 1: Register the flags**

In `internal/config/config.go` `ParseFlags`, add:

```go
	fs.StringVarP(&opts.Output.ContinueAt, "continue-at", "C", "", "Resume a partial download at OFFSET, or '-' for auto")
	fs.StringVar(&opts.Output.Range, "range", "", "Request only a byte RANGE (e.g. 0-1023)")
```

Add the `Range` field to `OutputOptions` (ContinueAt already added in Task 4):

```go
	Range    string
```

Add validation after the existing checks in `ParseFlags`:

```go
	if opts.Output.ContinueAt != "" && opts.Output.Range != "" {
		return nil, false, fmt.Errorf("--continue-at and --range cannot be used together")
	}
	if opts.Output.ContinueAt != "" && opts.Output.ContinueAt != "-" {
		if _, err := strconv.ParseInt(opts.Output.ContinueAt, 10, 64); err != nil {
			return nil, false, fmt.Errorf("invalid --continue-at value %q (want an integer offset or '-')", opts.Output.ContinueAt)
		}
	}
```

Add `"strconv"` to the config imports.

- [ ] **Step 2: Write the failing resume test**

Add to `internal/client/download_test.go`:

```go
func TestDownload_ResumeFromPartial(t *testing.T) {
	data := bytes.Repeat([]byte("RES"), 100000) // 300 KB
	streamer := blobServer(t, data)

	dir := t.TempDir()
	out := filepath.Join(dir, "resume.bin")
	// Pre-write the first 90000 bytes as an interrupted download.
	if err := os.WriteFile(out, data[:90000], 0644); err != nil {
		t.Fatal(err)
	}
	opts := &config.Options{
		URL: "http://test/resume.bin", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out, ContinueAt: "-"},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("resumed checksum mismatch (got %d, want %d)", len(got), len(data))
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestDownload_Resume -v`
Expected: FAIL — current `downloadSingle` truncates the file (`O_TRUNC`), so the result will be only the full body written from offset 0... actually it overwrites; checksum likely matches by luck. To make the assertion real, the test pre-writes a partial; with `O_TRUNC` resume is ignored and the server is asked for the whole file → still matches. So assert the resume path is taken via a Range-tracking server:

Use `countingBlobServer` and assert exactly the resume Range was requested:

```go
func TestDownload_ResumeRequestsRemainderOnly(t *testing.T) {
	data := bytes.Repeat([]byte("RES"), 100000) // 300 KB
	var gotStart int64 = -1
	ln := fasthttputil.NewInmemoryListener()
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
		start, end := parseTestRange(t, rng, len(data))
		atomic.StoreInt64(&gotStart, int64(start))
		ctx.SetStatusCode(206)
		ctx.Response.Header.Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
		ctx.SetBody(data[start : end+1])
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()
	c := &fasthttp.Client{StreamResponseBody: true, Dial: func(addr string) (net.Conn, error) { return ln.Dial() }}
	streamer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		rsp.StreamBody = true
		if err := c.Do(req, rsp); err != nil {
			return gholatransport.StreamMeta{}, err
		}
		meta := gholatransport.StreamMeta{StatusCode: rsp.Header.StatusCode(), ContentLength: int64(rsp.Header.ContentLength()), AcceptRanges: bytes.EqualFold(rsp.Header.Peek("Accept-Ranges"), []byte("bytes"))}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "resume.bin")
	os.WriteFile(out, data[:90000], 0644)
	opts := &config.Options{
		URL: "http://test/resume.bin", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out, ContinueAt: "-"},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if atomic.LoadInt64(&gotStart) != 90000 {
		t.Fatalf("resume Range start = %d, want 90000", atomic.LoadInt64(&gotStart))
	}
	got, _ := os.ReadFile(out)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("resumed checksum mismatch")
	}
}
```

- [ ] **Step 4: Implement resume + `--range` in `downloadSingle`**

Replace `downloadSingle` in `internal/client/download.go`:

```go
func downloadSingle(ctx context.Context, opts *config.Options, stream Streamer, url string) error {
	flags := os.O_CREATE | os.O_WRONLY
	var offset int64
	rangeHeader := ""

	switch {
	case opts.Output.ContinueAt != "":
		off, err := resumeOffset(opts)
		if err != nil {
			return err
		}
		offset = off
		flags |= os.O_APPEND
		rangeHeader = fmt.Sprintf("bytes=%d-", offset)
	case opts.Output.Range != "":
		rangeHeader = "bytes=" + strings.TrimPrefix(opts.Output.Range, "bytes=")
		flags |= os.O_TRUNC
	default:
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(opts.Output.File, flags, 0644)
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	defer f.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, opts.Data)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	meta, err := stream(ctx, opts, req, f)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if opts.Output.ContinueAt != "" && offset > 0 && meta.StatusCode == 200 {
		return fmt.Errorf("download: server ignored resume Range (status 200); cannot continue at %d", offset)
	}
	if meta.StatusCode < 200 || meta.StatusCode >= 300 {
		return fmt.Errorf("download: non-2xx status %d", meta.StatusCode)
	}
	return nil
}

// resumeOffset computes the resume byte offset: an explicit value, or the
// current file size when --continue-at is "-".
func resumeOffset(opts *config.Options) (int64, error) {
	if opts.Output.ContinueAt != "-" {
		return strconv.ParseInt(opts.Output.ContinueAt, 10, 64)
	}
	info, err := os.Stat(opts.Output.File)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // nothing downloaded yet; start from the beginning
		}
		return 0, fmt.Errorf("stat for resume: %w", err)
	}
	return info.Size(), nil
}
```

Add `"strconv"` to the `download.go` imports.

- [ ] **Step 5: Run the resume + config tests**

Run: `go test ./internal/client/ -run TestDownload_Resume -v && go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 6: Run the full suite + build**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/client/download.go internal/client/download_test.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(client): resume (-C) and explicit --range for streaming downloads"
```

---

## Task 6: Filename + timestamp flags (`-O`, `-J`, `-R`)

**Files:**
- Modify: `internal/config/config.go`, `internal/client/download.go`
- Test: `internal/config/config_test.go`, `internal/client/download_test.go`

**Interfaces:**
- Produces: `Options.Output.RemoteName` (`-O`), `Options.Output.RemoteHeader` (`-J`), `Options.Output.RemoteTime` (`-R`); `dispositionFilename(disposition string) string`; mtime application in `Download`.

- [ ] **Step 1: Register the flags + `-O` filename inference**

In `ParseFlags`, add fields to `OutputOptions`:

```go
	RemoteName   bool
	RemoteHeader bool
	RemoteTime   bool
```

Register:

```go
	fs.BoolVarP(&opts.Output.RemoteName, "remote-name", "O", false, "Write output to a file named like the remote file")
	fs.BoolVarP(&opts.Output.RemoteHeader, "remote-header-name", "J", false, "Use Content-Disposition filename for -O")
	fs.BoolVarP(&opts.Output.RemoteTime, "remote-time", "R", false, "Set the local file timestamp to the remote one")
```

After `inferWgetFilename` handling in `ParseFlags`, add `-O` inference (URL basename) when no explicit `-o`:

```go
	if opts.Output.RemoteName && opts.Output.File == "" {
		inferWgetFilename(opts) // reuse: derives basename from URL path
	}
```

- [ ] **Step 2: Write the failing test for `dispositionFilename`**

Add to `internal/client/download_test.go`:

```go
func TestDispositionFilename(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="report.pdf"`:        "report.pdf",
		`attachment; filename=data.bin`:            "data.bin",
		`inline`:                                   "",
		`attachment; filename="../../etc/passwd"`:  "passwd", // path stripped
		`attachment; filename="a/b/c.tar.gz"`:      "c.tar.gz",
	}
	for in, want := range cases {
		if got := dispositionFilename(in); got != want {
			t.Errorf("dispositionFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestDispositionFilename -v`
Expected: FAIL — `undefined: dispositionFilename`.

- [ ] **Step 4: Implement `dispositionFilename` and wire `-J`/`-R`**

Add to `internal/client/download.go`:

```go
// dispositionFilename extracts a safe basename from a Content-Disposition
// header value, or "" if none. Any directory components are stripped to
// prevent path traversal.
func dispositionFilename(disposition string) string {
	const marker = "filename="
	i := strings.Index(strings.ToLower(disposition), marker)
	if i < 0 {
		return ""
	}
	name := disposition[i+len(marker):]
	if j := strings.IndexByte(name, ';'); j >= 0 {
		name = name[:j]
	}
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"`)
	name = path.Base(name) // strip any directory components
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return name
}
```

Add `"path"` and `"time"` to the `download.go` imports.

In `Download`, after computing `meta` in `probe`, apply `-J` (rename target) before download and `-R` (set mtime) after. Modify `Download`:

```go
func Download(ctx context.Context, opts *config.Options, stream Streamer) error {
	if stream == nil {
		stream = DefaultStreamer
	}
	finalURL, meta, err := probe(ctx, opts, stream)
	if err != nil {
		return err
	}
	if opts.Output.RemoteHeader {
		if name := dispositionFilename(meta.Disposition); name != "" {
			opts.Output.File = name
		}
	}
	if canSegment(opts, meta) {
		if err := downloadSegmented(ctx, opts, stream, finalURL, meta.ContentLength); err != nil {
			return err
		}
	} else if err := downloadSingle(ctx, opts, stream, finalURL); err != nil {
		return err
	}
	if opts.Output.RemoteTime && meta.LastModified != "" {
		if ts, perr := time.Parse(time.RFC1123, meta.LastModified); perr == nil {
			_ = os.Chtimes(opts.Output.File, ts, ts)
		}
	}
	return nil
}
```

- [ ] **Step 5: Write the failing config test for `-O` inference**

Add to `internal/config/config_test.go`:

```go
func TestParseFlags_RemoteName(t *testing.T) {
	opts, done, err := ParseFlags([]string{"-O", "https://example.com/path/archive.tar.gz"})
	if err != nil || done {
		t.Fatalf("ParseFlags err=%v done=%v", err, done)
	}
	if opts.Output.File != "archive.tar.gz" {
		t.Errorf("File = %q, want archive.tar.gz", opts.Output.File)
	}
}
```

- [ ] **Step 6: Run the new tests**

Run: `go test ./internal/client/ -run TestDispositionFilename -v && go test ./internal/config/ -run TestParseFlags_RemoteName -v`
Expected: PASS.

- [ ] **Step 7: Build + full suite**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/client/download.go internal/client/download_test.go
git commit -m "feat: -O/-J remote filename and -R remote-time for downloads"
```

---

## Task 7: Rate limiting (`--limit-rate`) + progress

**Files:**
- Create: `internal/client/ratelimit.go`
- Modify: `internal/config/config.go`, `internal/config/defaults.go`, `internal/client/download.go`
- Test: `internal/client/ratelimit_test.go`

**Interfaces:**
- Produces: `parseRate(s string) (int64, error)`; `newRateLimitedWriter(w io.Writer, bytesPerSec int64) io.Writer`; `Options.Output.LimitRate`. Single-stream and each segment wrap their sink with the limiter when set.

- [ ] **Step 1: Write the failing test for `parseRate`**

Create `internal/client/ratelimit_test.go`:

```go
package client

import (
	"testing"
)

func TestParseRate(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1000", 1000, true},
		{"2k", 2000, true},
		{"2K", 2000, true},
		{"1m", 1000000, true},
		{"1M", 1000000, true},
		{"1g", 1000000000, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-5", 0, false},
	}
	for _, tc := range cases {
		got, err := parseRate(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("parseRate(%q) = (%d, %v), want (%d, nil)", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("parseRate(%q) expected error", tc.in)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestParseRate -v`
Expected: FAIL — `undefined: parseRate`.

- [ ] **Step 3: Implement `parseRate` and the limiter writer**

Create `internal/client/ratelimit.go`:

```go
// Package client — ratelimit.go provides a dependency-free token-bucket
// io.Writer for --limit-rate. curl semantics: an aggregate cap shared by all
// segments of a download.
package client

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// parseRate converts a human-readable rate ("500k", "2M", "1g", "1000")
// into bytes per second. Suffixes are decimal (k=1e3, m=1e6, g=1e9), matching
// curl's --limit-rate.
func parseRate(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty rate")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1_000, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1_000_000, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1_000_000_000, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid rate %q", s)
	}
	return n * mult, nil
}

// rateLimiter is a shared token bucket. Multiple writers (segments) draw from
// one limiter so the cap is aggregate.
type rateLimiter struct {
	mu          sync.Mutex
	bytesPerSec int64
	allowance   float64
	last        time.Time
}

func newRateLimiter(bytesPerSec int64) *rateLimiter {
	return &rateLimiter{bytesPerSec: bytesPerSec, allowance: float64(bytesPerSec), last: time.Now()}
}

// wait blocks until n bytes may be sent under the bucket.
func (rl *rateLimiter) wait(n int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for {
		now := time.Now()
		elapsed := now.Sub(rl.last).Seconds()
		rl.last = now
		rl.allowance += elapsed * float64(rl.bytesPerSec)
		if rl.allowance > float64(rl.bytesPerSec) {
			rl.allowance = float64(rl.bytesPerSec)
		}
		if rl.allowance >= float64(n) {
			rl.allowance -= float64(n)
			return
		}
		deficit := float64(n) - rl.allowance
		sleep := time.Duration(deficit / float64(rl.bytesPerSec) * float64(time.Second))
		rl.mu.Unlock()
		time.Sleep(sleep)
		rl.mu.Lock()
	}
}

type rateLimitedWriter struct {
	w  io.Writer
	rl *rateLimiter
}

func newRateLimitedWriter(w io.Writer, rl *rateLimiter) io.Writer {
	return &rateLimitedWriter{w: w, rl: rl}
}

func (rw *rateLimitedWriter) Write(p []byte) (int, error) {
	const chunk = 32 * 1024
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > chunk {
			n = chunk
		}
		rw.rl.wait(n)
		m, err := rw.w.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}
```

- [ ] **Step 4: Run the parseRate test**

Run: `go test ./internal/client/ -run TestParseRate -v`
Expected: PASS.

- [ ] **Step 5: Write the failing throughput test**

Add to `internal/client/ratelimit_test.go`:

```go
import (
	"bytes"
	"time"
	// keep "testing"
)

func TestRateLimitedWriter_Throttles(t *testing.T) {
	rl := newRateLimiter(100_000) // 100 KB/s
	var sink bytes.Buffer
	w := newRateLimitedWriter(&sink, rl)
	data := make([]byte, 50_000) // 0.5s worth

	start := time.Now()
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 300*time.Millisecond {
		t.Errorf("write took %v, expected >= ~0.5s of throttling", elapsed)
	}
	if sink.Len() != len(data) {
		t.Errorf("wrote %d bytes, want %d", sink.Len(), len(data))
	}
}
```

- [ ] **Step 6: Register the flag and wire the limiter into downloads**

In `ParseFlags`, add `LimitRate string` to `OutputOptions` and register:

```go
	fs.StringVar(&opts.Output.LimitRate, "limit-rate", "", "Limit transfer rate (e.g. 500k, 2M)")
```

Validate in `ParseFlags`:

```go
	if opts.Output.LimitRate != "" {
		if _, err := parseRateConfig(opts.Output.LimitRate); err != nil {
			return nil, false, fmt.Errorf("invalid --limit-rate %q", opts.Output.LimitRate)
		}
	}
```

Since `config` cannot import `client` (cycle), add a tiny local validator in `config.go`:

```go
// parseRateConfig validates a --limit-rate value during flag parsing. The
// authoritative parser lives in package client; this mirror is validation-only.
func parseRateConfig(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	switch s[len(s)-1] {
	case 'k', 'K', 'm', 'M', 'g', 'G':
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid")
	}
	return n, nil
}
```

Add `"strings"` to config imports if not present.

In `internal/client/download.go`, build a shared limiter in `Download` and pass it down. Add a field-free approach: compute the limiter once and wrap each sink. Modify `downloadSingle` and `fetchSegment` to accept an optional `*rateLimiter`:

- In `Download`, after `probe`:

```go
	var limiter *rateLimiter
	if opts.Output.LimitRate != "" {
		bps, err := parseRate(opts.Output.LimitRate)
		if err != nil {
			return fmt.Errorf("parse --limit-rate: %w", err)
		}
		limiter = newRateLimiter(bps)
	}
```

- Thread `limiter` into `downloadSegmented(ctx, opts, stream, finalURL, meta.ContentLength, limiter)` and `downloadSingle(ctx, opts, stream, finalURL, limiter)`; update their signatures and the `fetchSegment` signature. Where each writes to a sink, wrap it:

```go
	// single-stream sink:
	var sink io.Writer = f
	if limiter != nil {
		sink = newRateLimitedWriter(f, limiter)
	}
	meta, err := stream(ctx, opts, req, sink)
```

```go
	// segment sink:
	var w io.Writer = io.NewOffsetWriter(f, r.start)
	if limiter != nil {
		w = newRateLimitedWriter(w, limiter)
	}
	meta, err := stream(ctx, opts, req, w)
```

Update the resume/range branches in `downloadSingle` to use the wrapped `sink`. Update all call sites and the segmented goroutine closure to pass `limiter`.

- [ ] **Step 7: Run the new tests + full suite**

Run: `go test ./internal/client/ -run 'TestRateLimited|TestParseRate' -v && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/client/ratelimit.go internal/client/ratelimit_test.go internal/client/download.go internal/config/config.go
git commit -m "feat: --limit-rate aggregate rate limiting for downloads"
```

---

## Task 8: TLS flags (`-k`/`--insecure`, `--cacert`/`--cert`/`--key`)

**Files:**
- Modify: `internal/config/config.go`, `internal/transport/transport.go`
- Test: `internal/transport/transport_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `Options.Stealth.Insecure`, `Options.Stealth.CACert`, `Options.Stealth.ClientCert`, `Options.Stealth.ClientKey`; `transport.buildTLSConfig(opts *config.Options) (*tls.Config, error)` applied to both transports.

- [ ] **Step 1: Register the flags**

In `ParseFlags`, add to `StealthOptions`:

```go
	Insecure   bool
	CACert     string
	ClientCert string
	ClientKey  string
```

Register:

```go
	fs.BoolVarP(&opts.Stealth.Insecure, "insecure", "k", false, "Allow insecure server connections (skip TLS verification)")
	fs.StringVar(&opts.Stealth.CACert, "cacert", "", "CA certificate file to verify the peer")
	fs.StringVar(&opts.Stealth.ClientCert, "cert", "", "Client certificate file")
	fs.StringVar(&opts.Stealth.ClientKey, "key", "", "Private key file for the client certificate")
```

- [ ] **Step 2: Write the failing test for `buildTLSConfig`**

Add to `internal/transport/transport_test.go`:

```go
func TestBuildTLSConfig_Insecure(t *testing.T) {
	cfg, err := buildTLSConfig(&config.Options{Stealth: config.StealthOptions{Insecure: true}})
	if err != nil {
		t.Fatalf("buildTLSConfig error: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify true")
	}
}

func TestBuildTLSConfig_None(t *testing.T) {
	cfg, err := buildTLSConfig(&config.Options{})
	if err != nil {
		t.Fatalf("buildTLSConfig error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil tls.Config when no TLS flags set, got %+v", cfg)
	}
}
```

Add `"github.com/robot-accomplice/ghola/internal/config"` to the transport test imports if absent.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/transport/ -run TestBuildTLSConfig -v`
Expected: FAIL — `undefined: buildTLSConfig`.

- [ ] **Step 4: Implement `buildTLSConfig` and apply to both transports**

Add to `internal/transport/transport.go`:

```go
// buildTLSConfig assembles a *tls.Config from the TLS-related options, or
// returns nil when none are set (use the transport default). -k disables
// verification; --cacert pins a custom CA; --cert/--key add a client cert.
func buildTLSConfig(opts *config.Options) (*tls.Config, error) {
	if !opts.Stealth.Insecure && opts.Stealth.CACert == "" && opts.Stealth.ClientCert == "" {
		return nil, nil
	}
	cfg := &tls.Config{InsecureSkipVerify: opts.Stealth.Insecure} //nolint:gosec // opt-in via -k
	if opts.Stealth.CACert != "" {
		pem, err := os.ReadFile(opts.Stealth.CACert)
		if err != nil {
			return nil, fmt.Errorf("read --cacert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--cacert %q contains no valid certificates", opts.Stealth.CACert)
		}
		cfg.RootCAs = pool
	}
	if opts.Stealth.ClientCert != "" {
		if opts.Stealth.ClientKey == "" {
			return nil, fmt.Errorf("--cert requires --key")
		}
		cert, err := tls.LoadX509KeyPair(opts.Stealth.ClientCert, opts.Stealth.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
```

Add `"crypto/tls"`, `"crypto/x509"`, `"os"` to the transport imports.

In `New` (simpleTransport branch), apply it:

```go
	tlsCfg, err := buildTLSConfig(opts)
	if err != nil {
		return nil, err
	}
	client := &fasthttp.Client{
		ReadBufferSize:     opts.Resilience.BufferSize,
		StreamResponseBody: true,
		TLSConfig:          tlsCfg,
	}
```

In `newTLSClientTransport`, translate to tls-client options:

```go
	if opts.Stealth.Insecure {
		options = append(options, tlsclient.WithInsecureSkipVerify())
	}
	// Client certs and a custom CA are not supported on the impersonation
	// backend in this task; fail loud rather than silently ignoring them.
	if opts.Stealth.ClientCert != "" || opts.Stealth.CACert != "" {
		return nil, fmt.Errorf("--cert/--key/--cacert are not supported with --impersonate; use the default transport")
	}
```

> **Implementer note:** `WithInsecureSkipVerify` is confirmed present in the vendored `github.com/bogdanfinn/tls-client`. Before finalizing, run `go doc github.com/bogdanfinn/tls-client | grep -i 'cert\|tls'` — if the vendored version exposes a real client-cert/CA option, replace the error above with it instead of erroring. Do not invent an API name. Full `--cert`/`--cacert` support remains on the default (fasthttp) transport via `buildTLSConfig`.

- [ ] **Step 5: Run the TLS config tests**

Run: `go test ./internal/transport/ -run TestBuildTLSConfig -v`
Expected: PASS.

- [ ] **Step 6: Build + full suite**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/transport/transport.go internal/transport/transport_test.go
git commit -m "feat: -k/--insecure and --cacert/--cert/--key TLS options"
```

---

## Task 9: `--compressed` (gzip)

**Files:**
- Modify: `internal/config/config.go`, `internal/client/download.go`, `internal/output/output.go`
- Test: `internal/client/download_test.go`

**Interfaces:**
- Produces: `Options.Stealth.Compressed` (declared in Task 4, flag wired here). Sends `Accept-Encoding: gzip`; decodes gzip on both the streaming and buffered paths. Disables segmentation (already gated in `canSegment`).

- [ ] **Step 1: Register the flag**

In `ParseFlags` register (field `Compressed bool` already on `StealthOptions` from Task 4):

```go
	fs.BoolVar(&opts.Stealth.Compressed, "compressed", false, "Request a gzip-compressed response and decode it")
```

- [ ] **Step 2: Write the failing test (gzip body decoded on download)**

Add to `internal/client/download_test.go`:

```go
func TestDownload_CompressedGzip(t *testing.T) {
	plain := bytes.Repeat([]byte("zip-me-"), 10000)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(plain)
	zw.Close()
	gzData := gz.Bytes()

	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		if !strings.Contains(string(ctx.Request.Header.Peek("Accept-Encoding")), "gzip") {
			t.Error("Accept-Encoding gzip not sent")
		}
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Content-Encoding", "gzip")
		ctx.SetBody(gzData)
	}}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()
	c := &fasthttp.Client{StreamResponseBody: true, Dial: func(addr string) (net.Conn, error) { return ln.Dial() }}
	streamer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		rsp.StreamBody = true
		if err := c.Do(req, rsp); err != nil {
			return gholatransport.StreamMeta{}, err
		}
		meta := gholatransport.StreamMeta{StatusCode: rsp.Header.StatusCode(), ContentLength: int64(rsp.Header.ContentLength())}
		// emulate transport.Stream's responsibility: decode handled by caller via header
		if bytes.EqualFold(rsp.Header.Peek("Content-Encoding"), []byte("gzip")) {
			meta.GzipEncoded = true
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "z.txt")
	opts := &config.Options{
		URL: "http://test/z.txt", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: out},
		Stealth: config.StealthOptions{Agent: "ghola-test", Compressed: true},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, plain) {
		t.Fatalf("decoded body mismatch: got %d bytes, want %d", len(got), len(plain))
	}
}
```

Add `"compress/gzip"` to the test imports. This introduces `StreamMeta.GzipEncoded` — add it in Step 4.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/client/ -run TestDownload_Compressed -v`
Expected: FAIL — `meta.GzipEncoded undefined` and body still gzip.

- [ ] **Step 4: Add `GzipEncoded` to `StreamMeta`, send the header, decode on read**

In `internal/transport/transport.go`, add to `StreamMeta`:

```go
	GzipEncoded bool // Content-Encoding: gzip
```

Set it in both transports' `Stream` (and the test streamers): in `metaFromResponseHeader` add:

```go
	m.GzipEncoded = bytes.EqualFold(h.Peek("Content-Encoding"), []byte("gzip"))
```

(refactor `metaFromResponseHeader` to set the field on the struct before returning; for `tlsClientTransport.Stream` set `meta.GzipEncoded = strings.EqualFold(httpResp.Header.Get("Content-Encoding"), "gzip")`).

In `buildDownloadRequest` (download.go), send the request header when compression is requested:

```go
	if opts.Stealth.Compressed {
		req.Header.Set("Accept-Encoding", "gzip")
	}
```

In `download.go`, decode when the sink is written. Because streaming writes directly to the file, wrap the **file** in a gzip-decoding pipe for the single-stream path. Add a helper:

```go
// gzipSink returns a writer that transparently gunzips data written to it
// before passing it to dst, plus a flush func to call after streaming
// completes. It sniffs the gzip magic bytes (0x1f 0x8b): if the stream is NOT
// gzip (e.g. the server ignored Accept-Encoding), bytes pass through
// undecoded, so a non-compressing server never corrupts the output.
func gzipSink(dst io.Writer) (io.Writer, func() error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		br := bufio.NewReader(pr)
		magic, _ := br.Peek(2)
		if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
			zr, err := gzip.NewReader(br)
			if err != nil {
				pr.CloseWithError(err)
				done <- err
				return
			}
			_, cerr := io.Copy(dst, zr)
			zr.Close()
			done <- cerr
			return
		}
		// Not gzip: pass through verbatim.
		_, cerr := io.Copy(dst, br)
		done <- cerr
	}()
	return pw, func() error {
		pw.Close()
		return <-done
	}
}
```

Add `"bufio"` to the download.go imports.

Add `"compress/gzip"` to download.go imports. In `downloadSingle`, when `opts.Stealth.Compressed`, wrap the sink:

```go
	var flush func() error
	if opts.Stealth.Compressed {
		var gw io.Writer
		gw, flush = gzipSink(sink)
		sink = gw
	}
	meta, err := stream(ctx, opts, req, sink)
	if flush != nil {
		if ferr := flush(); ferr != nil && err == nil {
			err = ferr
		}
	}
```

(Place after the `sink` is established and rate-limiter wrapping. Order: file → rate-limit → gzip is wrong; gzip must wrap so that *compressed* bytes are rate-limited as received and decoded to the file. Correct order: `stream` writes compressed bytes → gzipSink decodes → writes plain to the rate-limited file sink. So wrap as: `fileSink = ratelimit(f)`, then `sink = gzipSink(fileSink)`.)

For the **buffered path** (`ProcessResponse`, when `--compressed` but not streamed, e.g. stdout), in `internal/output/output.go` after `body := rsp.Body()` add:

```go
	if opts.Stealth.Compressed && bytes.EqualFold(rsp.Header.Peek("Content-Encoding"), []byte("gzip")) {
		if decoded, err := rsp.BodyGunzip(); err == nil {
			body = decoded
		}
	}
```

Also set `Accept-Encoding: gzip` for the buffered path: in `client.go` `prepareRequest`, add:

```go
	if opts.Stealth.Compressed {
		req.Header.Set("Accept-Encoding", "gzip")
	}
```

- [ ] **Step 5: Run the compressed test + full suite**

Run: `go test ./internal/client/ -run TestDownload_Compressed -v && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/transport/transport.go internal/client/download.go internal/client/download_test.go internal/client/client.go internal/output/output.go
git commit -m "feat: --compressed (gzip) request and decode on stream + buffered paths"
```

---

## Task 10: Request-body flags (`-F`/`--form`, `--data-binary`, `--data-urlencode`)

**Files:**
- Modify: `internal/config/config.go`, `cmd/ghola/main.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Options.Form []string`, `Options.DataBinary string`, `Options.DataURLEncode []string`; `config.BuildFormBody(form []string) (contentType string, body []byte, err error)`; `config.URLEncodeData(pairs []string) string`. The CLI assembles the request body before dispatch.

- [ ] **Step 1: Register the flags + fields**

Add to `Options`:

```go
	Form          []string
	DataBinary    string
	DataURLEncode []string
```

Register in `ParseFlags`:

```go
	fs.StringArrayVarP(&opts.Form, "form", "F", nil, "Specify multipart form data (name=value or name=@file)")
	fs.StringVar(&opts.DataBinary, "data-binary", "", "HTTP POST binary data (no munging); @file reads verbatim")
	fs.StringArrayVar(&opts.DataURLEncode, "data-urlencode", nil, "URL-encode and POST the given data")
```

Add validation:

```go
	if len(opts.Form) > 0 && (opts.Data != "" || opts.DataBinary != "") {
		return nil, false, fmt.Errorf("--form cannot be combined with -d/--data-binary")
	}
```

When form/binary/urlencode supplied and `-X` not changed, default method to POST (extend the existing data→POST rule):

```go
	if (opts.Data != "" || opts.DataBinary != "" || len(opts.Form) > 0 || len(opts.DataURLEncode) > 0) && !fs.Changed("request") {
		opts.Method = fasthttp.MethodPost
	}
```

- [ ] **Step 2: Write the failing tests for the builders**

Add to `internal/config/config_test.go`:

```go
func TestURLEncodeData(t *testing.T) {
	got := URLEncodeData([]string{"q=a b", "x=1+2"})
	if got != "q=a+b&x=1%2B2" {
		t.Errorf("URLEncodeData = %q", got)
	}
}

func TestBuildFormBody_Fields(t *testing.T) {
	ct, body, err := BuildFormBody([]string{"name=ghola", "kind=cli"})
	if err != nil {
		t.Fatalf("BuildFormBody error: %v", err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(string(body), `name="name"`) || !strings.Contains(string(body), "ghola") {
		t.Errorf("body missing field: %s", body)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestURLEncodeData|TestBuildFormBody' -v`
Expected: FAIL — `undefined: URLEncodeData`, `undefined: BuildFormBody`.

- [ ] **Step 4: Implement the builders**

Create `internal/config/body.go`:

```go
// Package config — body.go builds request bodies for the curl-compatible
// data flags: --form (multipart), --data-urlencode.
package config

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"strings"
)

// URLEncodeData URL-encodes name=value pairs (the --data-urlencode flag) and
// joins them with '&'. A pair without '=' is encoded as a bare value.
func URLEncodeData(pairs []string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if i := strings.IndexByte(p, '='); i >= 0 {
			name, val := p[:i], p[i+1:]
			parts = append(parts, name+"="+url.QueryEscape(val))
		} else {
			parts = append(parts, url.QueryEscape(p))
		}
	}
	return strings.Join(parts, "&")
}

// BuildFormBody assembles a multipart/form-data body. Each entry is
// name=value, or name=@path to attach a file's contents. Returns the
// Content-Type (with boundary) and the encoded body.
func BuildFormBody(form []string) (string, []byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, entry := range form {
		i := strings.IndexByte(entry, '=')
		if i < 0 {
			return "", nil, fmt.Errorf("invalid --form %q (want name=value)", entry)
		}
		name, val := entry[:i], entry[i+1:]
		if strings.HasPrefix(val, "@") {
			path := val[1:]
			content, err := os.ReadFile(path)
			if err != nil {
				return "", nil, fmt.Errorf("read --form file %q: %w", path, err)
			}
			fw, err := mw.CreateFormFile(name, path)
			if err != nil {
				return "", nil, err
			}
			if _, err := fw.Write(content); err != nil {
				return "", nil, err
			}
		} else {
			if err := mw.WriteField(name, val); err != nil {
				return "", nil, err
			}
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return mw.FormDataContentType(), buf.Bytes(), nil
}
```

- [ ] **Step 5: Wire body assembly into the CLI**

In `cmd/ghola/main.go`, after the existing `opts.Data` stdin/file handling and before the streaming/concurrency routing, assemble the body:

```go
	if len(opts.Form) > 0 {
		ct, body, err := config.BuildFormBody(opts.Form)
		if err != nil {
			fmt.Fprintf(os.Stderr, "form: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		opts.Headers = append(opts.Headers, "Content-Type: "+ct)
		opts.Data = string(body)
	} else if opts.DataBinary != "" {
		data := opts.DataBinary
		if strings.HasPrefix(data, "@") {
			b, err := os.ReadFile(data[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "read --data-binary file: %v\n", err)
				return config.ReadFileFailed.Int()
			}
			data = string(b)
		}
		opts.Data = data
	} else if len(opts.DataURLEncode) > 0 {
		opts.Data = config.URLEncodeData(opts.DataURLEncode)
	}
```

Add `"strings"` to `cmd/ghola/main.go` imports.

> **Note:** `--form`/`--data-binary` produce non-GET requests, so `ShouldStream` returns false and they flow through the existing buffered pipeline — no download-path interaction.

- [ ] **Step 6: Run the new tests + full suite + build**

Run: `go test ./internal/config/ -run 'TestURLEncodeData|TestBuildFormBody' -v && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/body.go cmd/ghola/main.go
git commit -m "feat: -F/--form, --data-binary, --data-urlencode request bodies"
```

---

## Task 11: Help-text divergence note + docs

**Files:**
- Modify: `internal/config/config.go` (Usage), `docs/architecture/` if a CLI reference exists.

- [ ] **Step 1: Add the divergence note to `--help`**

In `ParseFlags`, extend `fs.Usage`:

```go
	fs.Usage = func() {
		fmt.Print(banner)
		fmt.Printf("ghola version %s\n", Version)
		fmt.Println("Usage: ghola [options...] <url>")
		fs.PrintDefaults()
		fmt.Println("\nNote: ghola's -c (--chain), -G (--ghost), and -b (--backoff) differ")
		fmt.Println("from curl's -c/-G/-b. Resume is -C/--continue-at.")
	}
```

- [ ] **Step 2: Verify help renders**

Run: `go run ./cmd/ghola --help`
Expected: usage prints, including the divergence note.

- [ ] **Step 3: Run the whole suite + vet + coverage check**

Run:
```bash
go vet ./...
go test ./... -cover
```
Expected: vet clean; each package ≥80% coverage. If a package regressed below 80% or its baseline, add focused tests for the uncovered new branches (probe fallback, redirect resolution, segment error propagation, TLS error cases) until the gate passes.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "docs: note curl flag-letter divergences in --help"
```

---

## Manual verification (after all tasks)

A real large-file smoke test (the scenario from the vault that triggered this work):

```bash
go build -o /tmp/ghola ./cmd/ghola
# Single-stream, bounded memory:
/usr/bin/time -l /tmp/ghola -L -o /tmp/big.bin https://<large-file-url>   # watch max RSS
# Segmented:
/usr/bin/time -l /tmp/ghola -n 8 -L -o /tmp/big2.bin https://<large-file-url>
# Resume: interrupt the first run (Ctrl-C), then:
/tmp/ghola -C - -L -o /tmp/big.bin https://<large-file-url>
# Verify both files match:
shasum -a 256 /tmp/big.bin /tmp/big2.bin
```

Expected: max RSS stays in the tens of MB (not the file size); `-n 8` is faster than `-n 1`; resume completes a partial; checksums match the source. This is the GREEN proof for the headline OOM bug (Rule 17 — reproduce/verify from real behavior, not plausibility).

---

## Self-review notes (coverage of the spec)

- Streaming core (OOM fix) → Tasks 1–2. Segmentation → Tasks 3–4. Resume + `--range` → Task 5. `-O`/`-J`/`-R` → Task 6. `--limit-rate` + progress → Task 7 (progress meter is folded into the streaming sink; if a visible meter is wanted it wraps the same sink — kept minimal per spec). TLS flags → Task 8. `--compressed` → Task 9. `-F`/`--data-binary`/`--data-urlencode` → Task 10. Divergence docs → Task 11.
- Deferrals honored: segmented resume falls back to single-stream resume (`canSegment` returns false when `ContinueAt != ""`); brotli excluded (`--compressed` is gzip-only).
- Bridge structs untouched; buffered pipeline behavior preserved (routing gated by `ShouldStream`).
