package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ErrConnectTimeout is returned when the connection phase (DNS, TCP, proxy
// CONNECT and TLS) takes longer than --connect-timeout.
var ErrConnectTimeout = errors.New("connection timed out")

// ErrRecv marks a request that failed after the connection was ready: DNS,
// TCP, the proxy CONNECT and TLS were done. curl reports this as a receive
// failure (exit 56), for example a TLS 1.3 alert that the server sends
// after the handshake, or a reset.
var ErrRecv = errors.New("receive failure")

// ErrTooSlow is returned when the transfer is slower than --speed-limit for
// --speed-time, as curl's "Operation too slow".
var ErrTooSlow = errors.New("operation too slow")

// speedTick is how often the speed check samples the byte count. A test
// can make it shorter before it builds a transport: newLimits copies it, so
// a running check never reads it.
var speedTick = time.Second

// limits holds curl's per-transfer limits other than --max-time, which the
// caller applies to ctx (client.WithTimeout).
type limits struct {
	connect    time.Duration
	speedLimit int64 // bytes per second
	speedTime  time.Duration
	tick       time.Duration
}

func newLimits(r config.ResilienceOptions) limits {
	return limits{connect: r.ConnectTimeout, speedLimit: r.SpeedLimit, speedTime: r.SpeedTime, tick: speedTick}
}

// watch is the limit state of one request. Both transports use it, so a limit
// cannot reach one stack and miss the other.
type watch struct {
	// causeOf reports why the request context ended. The watch keeps this
	// function, not the context: the context goes to the request only.
	causeOf func() error
	cancel  context.CancelCauseFunc
	bytes   atomic.Int64
	once    sync.Once
	conn    *time.Timer
	done    chan struct{}
	up      atomic.Bool // the connection was ready (GotConn)
}

// start returns the request context and the watch for one request. The
// request must use the returned context, call w.connected when the
// connection is ready (the GotConn trace event), pass its body through
// w.body, and pass a Do error through w.fail.
func (l limits) start(ctx context.Context) (context.Context, *watch) {
	ctx, cancel := context.WithCancelCause(ctx)
	w := &watch{causeOf: func() error { return context.Cause(ctx) }, cancel: cancel, done: make(chan struct{})}
	if l.connect > 0 {
		w.conn = time.AfterFunc(l.connect, func() {
			cancel(fmt.Errorf("%w after %d milliseconds", ErrConnectTimeout, l.connect.Milliseconds()))
		})
	}
	if l.speedLimit > 0 && l.speedTime > 0 {
		// The check starts with the request, so the wait for the headers
		// counts, as in curl 8.18.0 (checked against a server that holds
		// its headers for 3 s: curl stops after --speed-time with exit 28).
		go w.checkSpeed(l)
	}
	return ctx, w
}

// gotConn marks the connection ready and stops the connect timer. The
// GotConn trace event calls it, on net/http and on tls-client, after TLS.
func (w *watch) gotConn() {
	w.up.Store(true)
	w.connected()
}

// connected stops the connect timer.
func (w *watch) connected() {
	if w.conn != nil {
		w.conn.Stop()
	}
}

// stop ends the watch and frees its context.
func (w *watch) stop() {
	w.once.Do(func() {
		w.connected()
		close(w.done)
		w.cancel(nil)
	})
}

// cause returns the limit error that ended the request, or err.
func (w *watch) cause(err error) error {
	if c := w.causeOf(); errors.Is(c, ErrConnectTimeout) || errors.Is(c, ErrTooSlow) {
		return c
	}
	return err
}

// fail stops the watch after a failed request and returns the error to
// report. An error after the connection was ready is marked ErrRecv.
func (w *watch) fail(err error) error {
	err = w.cause(err)
	if w.up.Load() {
		err = fmt.Errorf("%w: %w", ErrRecv, err)
	}
	w.stop()
	return err
}

// checkSpeed stops the transfer when fewer than speedLimit bytes per second
// arrived over the last speedTime. It keeps one sample for each tick.
func (w *watch) checkSpeed(l limits) {
	ticker := time.NewTicker(l.tick)
	defer ticker.Stop()
	window := int(l.speedTime / l.tick)
	if window < 1 {
		window = 1
	}
	samples := []int64{0}
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
		}
		samples = append(samples, w.bytes.Load())
		if len(samples) <= window {
			continue
		}
		samples = samples[len(samples)-window-1:]
		if samples[window]-samples[0] < l.speedLimit*int64(l.speedTime/time.Second) {
			w.cancel(fmt.Errorf("%w: less than %d bytes/sec transferred the last %d seconds",
				ErrTooSlow, l.speedLimit, int64(l.speedTime/time.Second)))
			return
		}
	}
}

// body counts the bytes read for the speed check, reports a limit error in
// place of the context error, and ends the watch on Close.
func (w *watch) body(rc io.ReadCloser) io.ReadCloser {
	return &watchedBody{rc: rc, w: w}
}

type watchedBody struct {
	rc io.ReadCloser
	w  *watch
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.w.bytes.Add(int64(n))
	if err != nil && err != io.EOF {
		err = b.w.cause(err)
	}
	return n, err
}

func (b *watchedBody) Close() error {
	err := b.rc.Close()
	b.w.stop()
	return err
}
