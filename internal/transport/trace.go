package transport

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// traceBytesPerLine is the width of curl's --trace hex dump.
const traceBytesPerLine = 16

// tracer writes what ghola sends and receives, as curl --trace and
// --trace-ascii do: one block for each header and each piece of data,
// with the plain bytes, also on TLS. The segments of a download write
// at the same time, so the blocks lock.
type tracer struct {
	mu    sync.Mutex
	w     io.Writer
	ascii bool
	times bool
}

// block writes one block: "=> Send header, 78 bytes (0x4e)" and the dump.
func (t *tracer) block(what string, data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	if t.times {
		b.WriteString(time.Now().Format("15:04:05.000000 "))
	}
	fmt.Fprintf(&b, "%s, %d bytes (0x%x)\n", what, len(data), len(data))
	if t.ascii {
		off := 0
		for _, line := range strings.SplitAfter(string(data), "\n") {
			if line != "" {
				fmt.Fprintf(&b, "%04x: %s\n", off, strings.TrimRight(line, "\r\n"))
			}
			off += len(line)
		}
	} else {
		for off := 0; off < len(data); off += traceBytesPerLine {
			chunk := data[off:min(off+traceBytesPerLine, len(data))]
			fmt.Fprintf(&b, "%04x: ", off)
			for i := range traceBytesPerLine {
				if i < len(chunk) {
					fmt.Fprintf(&b, "%02x ", chunk[i])
				} else {
					b.WriteString("   ")
				}
			}
			for _, c := range chunk {
				if c < 0x20 || c > 0x7e {
					c = '.'
				}
				b.WriteByte(c)
			}
			b.WriteByte('\n')
		}
	}
	io.WriteString(t.w, b.String()) //nolint:errcheck // a trace that cannot be written must not stop the transfer
}

// request traces the request head as it goes on the wire: the request
// target and Host, then the headers. The stack may add a header of its
// own, such as Content-Length.
func (t *tracer) request(req *fasthttp.Request) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", req.Header.Method(), req.URI().RequestURI(), req.URI().Host())
	for k, v := range req.Header.All() {
		if !strings.EqualFold(string(k), "Host") {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	t.block("=> Send header", []byte(b.String()))
	t.block("=> Send data", req.Body())
}

type tracerKey struct{}

// tracedBody is a traced body that closes the stack's body.
type tracedBody struct {
	io.Reader
	io.Closer
}

// reader traces the body as ghola reads it.
func (t *tracer) reader(r io.Reader) io.Reader {
	return traceReader{r, t}
}

type traceReader struct {
	r io.Reader
	t *tracer
}

func (tr traceReader) Read(p []byte) (int, error) {
	n, err := tr.r.Read(p)
	tr.t.block("<= Recv data", p[:n])
	return n, err
}

// sink traces the head and the body of a streamed answer. It passes the
// header check on, which the download path needs before the body.
type traceSink struct {
	w io.Writer
	t *tracer
}

func (s traceSink) CheckHeaders(meta StreamMeta) error {
	s.t.block("<= Recv header", meta.HeaderBlock())
	if hc, ok := s.w.(HeaderChecker); ok {
		return hc.CheckHeaders(meta)
	}
	return nil
}

func (s traceSink) Write(p []byte) (int, error) {
	s.t.block("<= Recv data", p)
	return s.w.Write(p)
}
