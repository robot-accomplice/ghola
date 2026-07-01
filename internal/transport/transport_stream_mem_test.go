package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// zeroReader yields exactly n zero bytes without allocating a buffer of that
// size, so the test server can emit a large body cheaply.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	for i := range p {
		p[i] = 0
	}
	z.n -= int64(len(p))
	return len(p), nil
}

// TestSimpleTransport_StreamBoundedMemory asserts the default (fasthttp)
// transport's Stream writes the body to the sink WITHOUT first buffering the
// whole thing in RAM. A large fixed-Content-Length response streamed to
// io.Discard must allocate far less than the body size. This fails when the
// body is read fully into memory before BodyWriteTo/io.Copy runs (the v0.6.0
// OOM regression), and passes once Stream truly streams.
func TestSimpleTransport_StreamBoundedMemory(t *testing.T) {
	const bodySize = 256 << 20 // 256 MiB

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(bodySize, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = io.CopyN(w, &zeroReader{n: bodySize}, bodySize)
	}))
	defer srv.Close()

	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(srv.URL)
	req.Header.SetMethod("GET")

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	meta, err := tr.Stream(context.Background(), req, io.Discard)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if meta.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", meta.StatusCode)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc

	const limit = 64 << 20 // 64 MiB — a streaming copy uses a tiny reused buffer
	if allocated > limit {
		t.Fatalf("Stream allocated %d MiB for a %d MiB body — the body is being buffered in memory, not streamed to the sink",
			allocated>>20, int64(bodySize)>>20)
	}
}
