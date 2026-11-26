package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// requestLog records the method and URI of each request a test server gets.
type requestLog struct {
	mu   sync.Mutex
	reqs []string
}

func (l *requestLog) add(ctx *fasthttp.RequestCtx) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, string(ctx.Method())+" "+string(ctx.RequestURI()))
}

func (l *requestLog) count(method string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.reqs {
		if len(r) > len(method) && r[:len(method)+1] == method+" " {
			n++
		}
	}
	return n
}

// redirectingBlob serves data at /data, with Range support, and redirects
// /hop/N to /hop/N-1 and /hop/0 to /data.
func redirectingBlob(t *testing.T, data []byte, log *requestLog) Streamer {
	t.Helper()
	blob := blobHandler(t, data)
	ln, streamer := newStreamTestServer(func(ctx *fasthttp.RequestCtx) {
		log.add(ctx)
		path := string(ctx.Path())
		switch {
		case path == "/hop/0":
			ctx.Redirect("/data", fasthttp.StatusFound)
		case len(path) > 5 && path[:5] == "/hop/":
			n := path[5:]
			ctx.Redirect("/hop/"+itoa(atoiT(t, n)-1), fasthttp.StatusFound)
		default:
			blob(ctx)
		}
	})
	t.Cleanup(func() { ln.Close() })
	return streamer
}

// redirectOpts returns download options for url with n connections.
func redirectOpts(t *testing.T, url string, n int) *config.Options {
	opts := downloadOpts(filepath.Join(t.TempDir(), "f"), n, "")
	opts.URL = url
	opts.Resilience.Location, opts.Resilience.MaxRedirs = true, 5 // -L is the default of ghola download
	return opts
}

func checkFile(t *testing.T, name string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file has %d bytes, want %d", len(got), len(want))
	}
}

// TestDownload_SendsOneGETAndNoHEAD checks that a download to a file sends
// one GET and no HEAD (audit finding M5). A HEAD doubles the request count,
// and some servers answer HEAD differently from GET or refuse it.
func TestDownload_SendsOneGETAndNoHEAD(t *testing.T) {
	data := bytes.Repeat([]byte("h"), 4096)
	log := &requestLog{}
	opts := redirectOpts(t, "http://test/data", 1)
	if err := Download(context.Background(), opts, redirectingBlob(t, data, log)); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	checkFile(t, opts.Output.File, data)
	if log.count("HEAD") != 0 || len(log.reqs) != 1 {
		t.Errorf("requests = %q, want one GET", log.reqs)
	}
}

// TestDownload_FollowsRedirectsWithGETOnly checks that a redirect chain costs
// one GET for each hop and no HEAD.
func TestDownload_FollowsRedirectsWithGETOnly(t *testing.T) {
	data := bytes.Repeat([]byte("r"), 4096)
	log := &requestLog{}
	opts := redirectOpts(t, "http://test/hop/2", 1)
	if err := Download(context.Background(), opts, redirectingBlob(t, data, log)); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	checkFile(t, opts.Output.File, data)
	want := []string{"GET /hop/2", "GET /hop/1", "GET /hop/0", "GET /data"}
	if len(log.reqs) != len(want) {
		t.Fatalf("requests = %q, want %q", log.reqs, want)
	}
	for i := range want {
		if log.reqs[i] != want[i] {
			t.Fatalf("requests = %q, want %q", log.reqs, want)
		}
	}
}

// TestDownload_RedirectLimit checks that one hop more than --max-redirs is
// ErrTooManyRedirects on the download path, as on stdout (audit finding H6).
func TestDownload_RedirectLimit(t *testing.T) {
	log := &requestLog{}
	opts := redirectOpts(t, "http://test/hop/9", 1)
	opts.Resilience.MaxRedirs = 3
	err := Download(context.Background(), opts, redirectingBlob(t, []byte("x"), log))
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("Download = %v, want ErrTooManyRedirects", err)
	}
	if _, serr := os.Stat(opts.Output.File); !os.IsNotExist(serr) {
		t.Errorf("a failed redirect chain left a file (stat: %v)", serr)
	}
}

// TestDownload_SegmentedSendsNoHEAD checks that -n finds the size and the
// Range support with a ranged GET, not with HEAD, through a redirect.
func TestDownload_SegmentedSendsNoHEAD(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), config.DefaultSegmentMinBytes/8)
	log := &requestLog{}
	opts := redirectOpts(t, "http://test/hop/0", 4)
	if err := Download(context.Background(), opts, redirectingBlob(t, data, log)); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	checkFile(t, opts.Output.File, data)
	if log.count("HEAD") != 0 {
		t.Errorf("requests = %q, want no HEAD", log.reqs)
	}
	// The probe hop, the probe, and 4 segments.
	if log.count("GET") != 6 {
		t.Errorf("requests = %q, want 6 GETs", log.reqs)
	}
}

// TestDownload_RetriesARetryableStatus checks that -r with --retry-http
// retries a 503 on the download path, as on stdout, and that the 503 body
// never reaches the file. Before M5, two HEAD requests used up the 503
// answers, so a download seemed to retry when it did not.
func TestDownload_RetriesARetryableStatus(t *testing.T) {
	var calls atomic.Int32
	ln, streamer := newStreamTestServer(func(ctx *fasthttp.RequestCtx) {
		if calls.Add(1) <= 2 {
			ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
			ctx.SetBodyString("busy")
			return
		}
		ctx.SetBodyString("ok")
	})
	defer ln.Close()
	opts := redirectOpts(t, "http://test/flaky", 1)
	opts.Resilience.Retries, opts.Resilience.RetryHTTP, opts.Resilience.Backoff = 3, true, 1
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	checkFile(t, opts.Output.File, []byte("ok"))
	if got := calls.Load(); got != 3 {
		t.Errorf("server got %d requests, want 3", got)
	}
}

// TestDownload_RetriesAFailedConnection checks that -r retries a request
// that failed before it had a response, as FetchURL does.
func TestDownload_RetriesAFailedConnection(t *testing.T) {
	good := blobServer(t, []byte("data"))
	var calls atomic.Int32
	streamer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		if calls.Add(1) == 1 {
			return gholatransport.StreamMeta{}, errors.New("connection refused")
		}
		return good(ctx, opts, req, sink)
	}
	opts := redirectOpts(t, "http://test/data", 1)
	opts.Resilience.Retries, opts.Resilience.Backoff = 1, 1
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	checkFile(t, opts.Output.File, []byte("data"))
}

// TestDownload_RetryWaitsForRetryAfter checks that a download retry waits
// for the server's Retry-After, as FetchURL does, and not only --backoff.
func TestDownload_RetryWaitsForRetryAfter(t *testing.T) {
	var calls atomic.Int32
	ln, streamer := newStreamTestServer(func(ctx *fasthttp.RequestCtx) {
		if calls.Add(1) == 1 {
			ctx.Response.Header.Set("Retry-After", "1")
			ctx.SetStatusCode(fasthttp.StatusServiceUnavailable)
			return
		}
		ctx.SetBodyString("ok")
	})
	defer ln.Close()
	opts := redirectOpts(t, "http://test/flaky", 1)
	opts.Resilience.Retries, opts.Resilience.RetryHTTP, opts.Resilience.Backoff = 1, true, 1
	start := time.Now()
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("retry came after %v, want the 1 s Retry-After", elapsed)
	}
	checkFile(t, opts.Output.File, []byte("ok"))
}
