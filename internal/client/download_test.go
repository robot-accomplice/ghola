package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

func itoa(n int) string { return strconv.Itoa(n) }

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
		{5, 4, []byteRange{{0, 1}, {2, 3}, {4, 4}}},  // ceiling early-break: 3 < 4 parts
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
		meta := gholatransport.StreamMeta{
			StatusCode:    rsp.Header.StatusCode(),
			ContentLength: int64(rsp.Header.ContentLength()),
			AcceptRanges:  bytes.EqualFold(rsp.Header.Peek("Accept-Ranges"), []byte("bytes")),
		}
		_ = rsp.BodyWriteTo(sink)
		return meta, nil
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "resume.bin")
	os.WriteFile(out, data[:90000], 0644) //nolint:errcheck
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

func TestDispositionFilename(t *testing.T) {
	cases := map[string]string{
		`attachment; filename="report.pdf"`:       "report.pdf",
		`attachment; filename=data.bin`:           "data.bin",
		`inline`:                                  "",
		`attachment; filename="../../etc/passwd"`: "passwd", // path stripped
		`attachment; filename="a/b/c.tar.gz"`:     "c.tar.gz",
		`attachment; filename="/etc/shadow"`:      "shadow", // absolute path stripped
	}
	for in, want := range cases {
		if got := dispositionFilename(in); got != want {
			t.Errorf("dispositionFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

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
