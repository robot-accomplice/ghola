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
