package client

// These tests reproduce findings C2 and C3 of the 2026-11-12 audit: a server
// that advertises byte ranges and then ignores them, or sends the wrong
// range, must never produce a corrupt file that ghola reports as a success.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// rangeLiar answers like a range-capable server on HEAD, but answers each
// Range GET with ans(start, end) instead of the requested bytes.
type rangeAnswer func(ctx *fasthttp.RequestCtx, data []byte, start, end int)

// ignoreRange sends 200 and the full body, as the audit's /liar endpoint did.
func ignoreRange(ctx *fasthttp.RequestCtx, data []byte, _, _ int) {
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(data)
}

// shiftRange sends 206 with a Content-Range that starts 1000 bytes too late,
// as the audit's /badrange endpoint did.
func shiftRange(ctx *fasthttp.RequestCtx, data []byte, start, end int) {
	start += 1000
	if end < start {
		end = start
	}
	ctx.SetStatusCode(fasthttp.StatusPartialContent)
	ctx.Response.Header.Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
	ctx.SetBody(data[start : end+1])
}

func rangeLiar(t *testing.T, data []byte, ans rangeAnswer) Streamer {
	t.Helper()
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Accept-Ranges", "bytes")
		rng := string(ctx.Request.Header.Peek("Range"))
		if rng == "" || ctx.IsHead() {
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.Header.SetContentLength(len(data))
			if !ctx.IsHead() {
				ctx.SetBody(data)
			}
			return
		}
		start, end := parseTestRange(t, rng, len(data))
		ans(ctx, data, start, end)
	}}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { ln.Close() })
	c := &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}
	return func(_ context.Context, _ *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		if err := c.Do(req, rsp); err != nil {
			return gholatransport.StreamMeta{}, err
		}
		return deliverFasthttp(rsp, sink)
	}
}

func testData() []byte { return bytes.Repeat([]byte("0123456789abcdef"), 1<<17) } // 2 MiB

func downloadOpts(out string, conc int, continueAt string) *config.Options {
	return &config.Options{
		URL: "http://test/f", Method: "GET", Concurrency: conc,
		Output:  config.OutputOptions{File: out, Silent: true, ContinueAt: continueAt},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
}

// C2: aria2 falls back and writes the right file; ghola wrote 14.7 MB for 8 MB.
func TestDownload_SegmentedFallsBackWhenServerIgnoresRange(t *testing.T) {
	data := testData()
	out := filepath.Join(t.TempDir(), "f")
	if err := Download(context.Background(), downloadOpts(out, 4, ""), rangeLiar(t, data, ignoreRange)); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, data) {
		t.Fatalf("file has %d bytes, want the exact %d-byte body", len(got), len(data))
	}
}

// C2: a wrong Content-Range must fail, and must not leave a full-size file
// that a later resume would take for a finished download.
func TestDownload_SegmentedRejectsWrongContentRange(t *testing.T) {
	out := filepath.Join(t.TempDir(), "f")
	err := Download(context.Background(), downloadOpts(out, 4, ""), rangeLiar(t, testData(), shiftRange))
	if err == nil || !strings.Contains(err.Error(), "requested") {
		t.Fatalf("Download error = %v, want a range mismatch error", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("output file still exists after a failed segmented download (stat err %v)", statErr)
	}
}

// C3: curl refuses both answers and leaves the partial file as it was.
func TestDownload_ResumeKeepsPartialFileOnBadAnswer(t *testing.T) {
	for name, ans := range map[string]rangeAnswer{"ignored": ignoreRange, "shifted": shiftRange} {
		t.Run(name, func(t *testing.T) {
			data := testData()
			partial := data[:300000]
			out := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(out, partial, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Download(context.Background(), downloadOpts(out, 1, "-"), rangeLiar(t, data, ans)); err == nil {
				t.Fatal("Download succeeded, want an error")
			}
			got, _ := os.ReadFile(out)
			if !bytes.Equal(got, partial) {
				t.Fatalf("partial file changed: %d bytes, want the original %d", len(got), len(partial))
			}
		})
	}
}

func TestCheckedSink_RefusesBodyBeforeHeaderCheck(t *testing.T) {
	var buf bytes.Buffer
	s := newCheckedSink(&buf, func(gholatransport.StreamMeta) error { return nil })
	if _, err := s.Write([]byte("x")); !errors.Is(err, errUncheckedBody) {
		t.Fatalf("Write before CheckHeaders: err = %v, want errUncheckedBody", err)
	}
	if buf.Len() != 0 {
		t.Fatal("bytes reached the destination before the header check")
	}
}

func TestExpectRange(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		cr         string
		start, end int64
		wantErr    bool
	}{
		{"exact", 206, "bytes 10-19/100", 10, 19, false},
		{"open end", 206, "bytes 10-99/*", 10, -1, false},
		{"full body", 200, "", 10, 19, true},
		{"wrong start", 206, "bytes 11-19/100", 10, 19, true},
		{"wrong end", 206, "bytes 10-29/100", 10, 19, true},
		{"missing", 206, "", 10, 19, true},
		{"not bytes", 206, "items 10-19/100", 10, 19, true},
		{"reversed", 206, "bytes 19-10/100", 10, 19, true},
		{"error status", 416, "bytes */100", 10, 19, true},
	}
	for _, tc := range cases {
		err := expectRange(tc.start, tc.end)(gholatransport.StreamMeta{StatusCode: tc.status, ContentRange: tc.cr})
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
