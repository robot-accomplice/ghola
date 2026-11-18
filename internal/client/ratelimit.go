// Package client — ratelimit.go provides a dependency-free token-bucket
// io.Writer for --limit-rate (curl semantics: an aggregate cap shared across
// all segments of a download) and a throttled stderr progress meter.
package client

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// progressInterval is the minimum time between progress line updates.
const progressInterval = 200 * time.Millisecond

// rateLimitChunk is the maximum bytes sent per token-bucket iteration so that
// large writes are broken into bounded slices and the limiter stays responsive.
const rateLimitChunk = 32 * 1024

// rateLimiter is a shared token bucket. Multiple writers (segments) draw from
// one limiter so the cap is aggregate across all concurrent goroutines.
type rateLimiter struct {
	mu          sync.Mutex
	bytesPerSec int64
	allowance   float64
	last        time.Time
}

func newRateLimiter(bytesPerSec int64) *rateLimiter {
	return &rateLimiter{
		bytesPerSec: bytesPerSec,
		allowance:   0, // cold start: no credit; first write pays immediately
		last:        time.Now(),
	}
}

// wait blocks until n bytes may be sent under the token bucket.
func (rl *rateLimiter) wait(n int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for {
		now := time.Now()
		elapsed := now.Sub(rl.last).Seconds()
		rl.last = now
		rl.allowance += elapsed * float64(rl.bytesPerSec)
		if rl.allowance > float64(rl.bytesPerSec) {
			rl.allowance = float64(rl.bytesPerSec)
		}
		if rl.allowance >= float64(n) {
			rl.allowance -= float64(n)
			return
		}
		deficit := float64(n) - rl.allowance
		sleep := time.Duration(deficit / float64(rl.bytesPerSec) * float64(time.Second))
		rl.mu.Unlock()
		time.Sleep(sleep)
		rl.mu.Lock()
	}
}

// chunk returns the most bytes that one wait may ask for. The bucket holds
// at most one second of bytes, so a larger request could never pass.
func (rl *rateLimiter) chunk() int {
	return int(min(rateLimitChunk, rl.bytesPerSec))
}

// LimitReader returns r throttled to --limit-rate, or r when the flag is not
// set. The stdout path uses it, so the limit applies there as it does to a
// download to a file (audit finding M3).
func LimitReader(r io.Reader, opts *config.Options) (io.Reader, error) {
	if opts.Output.LimitRate == "" {
		return r, nil
	}
	bps, err := config.ParseRate(opts.Output.LimitRate)
	if err != nil {
		return nil, fmt.Errorf("parse --limit-rate: %w", err)
	}
	return &rateLimitedReader{r: r, rl: newRateLimiter(bps)}, nil
}

// rateLimitedReader throttles reads through a rateLimiter.
type rateLimitedReader struct {
	r  io.Reader
	rl *rateLimiter
}

func (rr *rateLimitedReader) Read(p []byte) (int, error) {
	if n := rr.rl.chunk(); len(p) > n {
		p = p[:n]
	}
	n, err := rr.r.Read(p)
	rr.rl.wait(n)
	return n, err
}

// rateLimitedWriter wraps an io.Writer and throttles writes through a shared
// rateLimiter. Safe for concurrent use from multiple goroutines (segments).
type rateLimitedWriter struct {
	w  io.Writer
	rl *rateLimiter
}

func newRateLimitedWriter(w io.Writer, rl *rateLimiter) io.Writer {
	return &rateLimitedWriter{w: w, rl: rl}
}

func (rw *rateLimitedWriter) Write(p []byte) (int, error) {
	// Cap chunk to the bucket capacity so wait(n) is always satisfiable.
	// Without this cap, any --limit-rate below rateLimitChunk (32 KB/s)
	// would cause wait to loop forever because allowance is capped at
	// bytesPerSec and can never reach n.
	maxChunk := rw.rl.chunk()
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxChunk {
			n = maxChunk
		}
		rw.rl.wait(n)
		m, err := rw.w.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// progressWriter counts bytes written to dst and periodically prints a
// single-line \r-terminated progress report to w (stderr). total is -1 when
// the content length is unknown. Write is mutex-guarded so multiple segment
// goroutines can share one instance for an aggregate count.
//
// NOT safe for concurrent direct Write calls: Write reads dst outside the
// mutex, so two goroutines calling Write simultaneously would race on dst.
// The concurrent/segmented download path must use progressTap instead, which
// drives each segment's real sink independently and only touches the shared
// counter under the mutex.
type progressWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	w       io.Writer // progress output destination (stderr)
	total   int64
	written int64
	start   time.Time
	last    time.Time
}

func newProgressWriter(dst, w io.Writer, total int64) *progressWriter {
	now := time.Now()
	return &progressWriter{dst: dst, w: w, total: total, start: now, last: now}
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.dst.Write(b)
	p.add(n)
	return n, err
}

// add counts n bytes and prints a meter line when progressInterval has passed.
func (p *progressWriter) add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written += int64(n)
	if now := time.Now(); now.Sub(p.last) >= progressInterval {
		p.report(now)
	}
}

// setTotal sets the total from the accepted response. A probe of the URL can
// give a different length than the response that is written (audit finding M4).
func (p *progressWriter) setTotal(total int64) {
	p.mu.Lock()
	p.total = total
	p.mu.Unlock()
}

// report prints one meter line. The caller holds p.mu.
func (p *progressWriter) report(now time.Time) {
	p.last = now
	secs := now.Sub(p.start).Seconds()
	var rate float64
	if secs > 0 {
		rate = float64(p.written) / secs
	}
	if p.total > 0 {
		fmt.Fprintf(p.w, "\r%6.1f%%  %d/%d bytes  %.1f MB/s   ",
			100*float64(p.written)/float64(p.total), p.written, p.total, rate/1e6)
	} else {
		fmt.Fprintf(p.w, "\r%d bytes  %.1f MB/s   ", p.written, rate/1e6)
	}
}

// done prints the final meter line and a newline.
func (p *progressWriter) done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report(time.Now())
	fmt.Fprintln(p.w)
}

// progressTap is a per-segment io.Writer that forwards bytes to a local sink
// while counting them into a shared progressWriter. This avoids the data race
// of mutating progressWriter.dst from multiple goroutines: each segment gets
// its own tap pointing at its own offset writer, all counting into one shared
// progressWriter.
type progressTap struct {
	dst      io.Writer       // this segment's real sink (offset writer, possibly rate-limited)
	progress *progressWriter // shared aggregate counter
}

func newProgressTap(dst io.Writer, progress *progressWriter) io.Writer {
	return &progressTap{dst: dst, progress: progress}
}

func (pt *progressTap) Write(b []byte) (int, error) {
	n, err := pt.dst.Write(b)
	if n > 0 {
		pt.progress.add(n)
	}
	return n, err
}
