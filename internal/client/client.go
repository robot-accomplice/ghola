// Package client provides the HTTP request engine for ghola, including
// retry with exponential backoff, temporal drift jitter, ghost identity
// signing, and concurrent request execution with clean shutdown.
package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// Doer abstracts the HTTP round-trip so tests can inject a mock transport.
type Doer func(req *fasthttp.Request, resp *fasthttp.Response, bufferSize int) error

// DefaultDoer uses fasthttp.Do for real network calls.
func DefaultDoer(req *fasthttp.Request, resp *fasthttp.Response, bufferSize int) error {
	c := &fasthttp.Client{
		ReadBufferSize: bufferSize,
	}
	return c.Do(req, resp)
}

// FetchURL sends an HTTP request according to opts, with retry and jitter.
// The context controls cancellation of retry backoff and drift sleeps; a
// cancelled context causes FetchURL to return ctx.Err() immediately.
// The caller must release the returned Request and Response via
// fasthttp.ReleaseRequest / ReleaseResponse.
func FetchURL(ctx context.Context, opts *config.Options, do Doer) (*fasthttp.Request, *fasthttp.Response, error) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()

	var lastErr error
	for attempt := 0; attempt <= opts.Retries; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}

		if attempt > 0 {
			wait := time.Duration(opts.Backoff<<uint(attempt-1)) * time.Millisecond
			if !opts.Silent {
				fmt.Fprintf(os.Stderr, "Retry %d/%d in %v...\n", attempt, opts.Retries, wait)
			}
			if err := sleepCtx(ctx, wait); err != nil {
				lastErr = err
				break
			}
		}

		req.Reset()
		rsp.Reset()

		if opts.Drift > 0 {
			if err := applyDrift(ctx, opts.Drift); err != nil {
				lastErr = err
				break
			}
		}

		req.SetRequestURI(opts.URL)
		req.SetBodyString(opts.Data)

		for _, h := range opts.Headers {
			parts := strings.SplitN(h, ": ", 2)
			if len(parts) == 2 {
				req.Header.Add(parts[0], parts[1])
			}
		}
		req.Header.Add("User-Agent", opts.Agent)

		if opts.Ghost {
			applyGhostSign(req, opts.URL)
		}

		if opts.User != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(opts.User))
			req.Header.Set("Authorization", "Basic "+auth)
		}

		req.Header.SetMethod(opts.Method)

		lastErr = do(req, rsp, opts.BufferSize)
		if lastErr == nil {
			return req, rsp, nil
		}
	}

	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(rsp)
	return nil, nil, lastErr
}

// sleepCtx blocks for d or until ctx is cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func applyDrift(ctx context.Context, driftMs int) error {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(driftMs)))
	if err != nil {
		return nil
	}
	return sleepCtx(ctx, time.Duration(n.Int64())*time.Millisecond)
}

func applyGhostSign(req *fasthttp.Request, rawURL string) {
	id := fmt.Sprintf("%d-%s", time.Now().Unix(), rawURL)
	hash := sha256.Sum256([]byte(id))
	req.Header.Set("X-Ghola-Identity", hex.EncodeToString(hash[:]))
}

// RunConcurrent spawns opts.Concurrency goroutines, each calling FetchURL.
// The first successful response is passed to out; the remaining goroutines
// are cancelled immediately via the or-done pattern (context cancellation)
// so they stop retrying and release resources.
func RunConcurrent(ctx context.Context, opts *config.Options, do Doer, out func(*fasthttp.Request, *fasthttp.Response)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg   sync.WaitGroup
		once sync.Once
	)
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, rsp, err := FetchURL(ctx, opts, do)
			if err != nil {
				return
			}
			once.Do(func() {
				out(req, rsp)
				cancel()
			})
		}()
	}
	wg.Wait()
}
