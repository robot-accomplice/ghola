package output

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// --max-size counts the bytes that ghola writes, after the decoder, on
// stdout as on -o. A small gzip body that decodes to much more than the
// limit must stop at the limit; on stdout it passed, because the limit
// counted the compressed bytes.
func TestMaxSizeCountsDecodedBytesOnStdout(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(bytes.Repeat([]byte("a"), 1<<20)) //nolint:errcheck
	zw.Close()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetStatusCode(200)
	rsp.Header.Set("Content-Encoding", "gzip")
	rsp.SetBody(gz.Bytes())
	opts := &config.Options{Stealth: config.StealthOptions{Compressed: true},
		Output: config.OutputOptions{MaxSize: 64 << 10}}
	if gz.Len() >= int(opts.Output.MaxSize) {
		t.Fatalf("the test body is %d bytes compressed; it must be under the limit", gz.Len())
	}
	err := ProcessResponse(io.Discard, io.Discard, opts, nil, rsp, 0)
	if !errors.Is(err, config.ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge (exit 63)", err)
	}
}
