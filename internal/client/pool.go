package client

import (
	"context"
	"io"
	"sync"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Pool keeps one transport for each proxy that the options select, so a
// run of many requests with the same options reuses its connections.
// DefaultStreamer and DefaultDoer build a new transport for each request:
// each one opens a new connection, and its idle connection stays open
// until the process ends. Use a Pool only for requests whose options
// differ in the URL, the headers and the output, not in the network
// options.
type Pool struct {
	mu  sync.Mutex
	all map[string]gholatransport.Transport
}

func (p *Pool) get(opts *config.Options) (gholatransport.Transport, error) {
	proxy, err := selectProxy(opts)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tr, ok := p.all[proxy]; ok {
		return tr, nil
	}
	tr, err := gholatransport.New(opts, proxy)
	if err != nil {
		return nil, err
	}
	if p.all == nil {
		p.all = map[string]gholatransport.Transport{}
	}
	p.all[proxy] = tr
	return tr, nil
}

// Stream is a Streamer that uses the transport of the pool.
func (p *Pool) Stream(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
	tr, err := p.get(opts)
	if err != nil {
		return gholatransport.StreamMeta{}, err
	}
	return tr.Stream(ctx, req, sink)
}

// Do is a Doer that uses the transport of the pool.
func (p *Pool) Do(ctx context.Context, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response) error {
	tr, err := p.get(opts)
	if err != nil {
		return err
	}
	return tr.Do(ctx, req, rsp)
}
