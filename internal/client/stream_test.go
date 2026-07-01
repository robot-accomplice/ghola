package client

import (
	"bytes"
	"context"
	"io"
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
