package output

import (
	"bytes"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// --compressed decodes a br body on stdout too, by Content-Encoding.
func TestProcessResponse_CompressedBrotli(t *testing.T) {
	want := bytes.Repeat([]byte("brotli on stdout "), 300)
	var enc bytes.Buffer
	bw := brotli.NewWriter(&enc)
	bw.Write(want) //nolint:errcheck
	bw.Close()
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	rsp.Header.Set("Content-Encoding", "br")
	rsp.SetBody(enc.Bytes())
	var out bytes.Buffer
	opts := &config.Options{Stealth: config.StealthOptions{Compressed: true}}
	if err := ProcessResponse(&out, io.Discard, opts, req, rsp, 0); err != nil || !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("%d bytes, err %v", out.Len(), err)
	}
}
