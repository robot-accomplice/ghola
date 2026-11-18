package client

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
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
	opts.Resilience.MaxRedirs = 5
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
	if got := log.count("GET"); got != 6 {
		t.Errorf("requests = %q, want 6 GETs", log.reqs)
	}
}
