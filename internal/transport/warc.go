package transport

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// warcWriter writes the exchanges as WARC/1.1 records (ISO 28500), as
// wget --warc-file does: a warcinfo record first, then a request and a
// response record for each exchange. The parallel transfers lock it.
type warcWriter struct {
	mu      sync.Mutex
	w       io.Writer
	started bool
	version string
}

// warcSink copies the body, as it comes, to a temporary file: a WARC
// record gives its length before its block. It passes the header check
// on, which the download path needs before the body.
type warcSink struct {
	w   io.Writer
	tmp *os.File
}

func (s warcSink) CheckHeaders(meta StreamMeta) error {
	if hc, ok := s.w.(HeaderChecker); ok {
		return hc.CheckHeaders(meta)
	}
	return nil
}

func (s warcSink) Write(p []byte) (int, error) {
	if _, err := s.tmp.Write(p); err != nil {
		return 0, fmt.Errorf("--warc: %w", err)
	}
	return s.w.Write(p)
}

// record writes the request and the response of one exchange.
func (ww *warcWriter) record(req *fasthttp.Request, meta StreamMeta, body *os.File) error {
	size, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	ww.mu.Lock()
	defer ww.mu.Unlock()
	if !ww.started {
		fields := "software: ghola/" + ww.version + "\r\nformat: WARC File Format 1.1\r\n"
		if err := ww.write("warcinfo", "", "application/warc-fields", "", strings.NewReader(fields), int64(len(fields))); err != nil {
			return err
		}
		ww.started = true
	}
	target := string(req.URI().FullURI())
	respID := warcID()
	head := meta.HeaderBlock()
	if err := ww.write("response", target, "application/http;msgtype=response", respID,
		io.MultiReader(strings.NewReader(string(head)), body), int64(len(head))+size); err != nil {
		return err
	}
	reqHead := requestHead(req)
	return ww.write("request", target, "application/http;msgtype=request", "<"+respID+">",
		io.MultiReader(strings.NewReader(reqHead), strings.NewReader(string(req.Body()))), int64(len(reqHead)+len(req.Body())))
}

// write writes one record. For a response, id is the record's own ID;
// for another record, it is the response it belongs to, or "".
func (ww *warcWriter) write(kind, target, contentType, id string, block io.Reader, length int64) error {
	var h strings.Builder
	h.WriteString("WARC/1.1\r\nWARC-Type: " + kind + "\r\n")
	switch kind {
	case "response":
		h.WriteString("WARC-Record-ID: <" + id + ">\r\n")
	default:
		h.WriteString("WARC-Record-ID: <" + warcID() + ">\r\n")
		if id != "" {
			h.WriteString("WARC-Concurrent-To: " + id + "\r\n")
		}
	}
	h.WriteString("WARC-Date: " + time.Now().UTC().Format("2006-01-02T15:04:05Z") + "\r\n")
	if target != "" {
		h.WriteString("WARC-Target-URI: " + target + "\r\n")
	}
	fmt.Fprintf(&h, "Content-Type: %s\r\nContent-Length: %d\r\n\r\n", contentType, length)
	if _, err := io.WriteString(ww.w, h.String()); err != nil {
		return fmt.Errorf("--warc: %w", err)
	}
	if _, err := io.Copy(ww.w, block); err != nil {
		return fmt.Errorf("--warc: %w", err)
	}
	if _, err := io.WriteString(ww.w, "\r\n\r\n"); err != nil {
		return fmt.Errorf("--warc: %w", err)
	}
	return nil
}

// requestHead is the request head as it goes on the wire.
func requestHead(req *fasthttp.Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", req.Header.Method(), req.URI().RequestURI(), req.URI().Host())
	for k, v := range req.Header.All() {
		if !strings.EqualFold(string(k), "Host") {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	return b.String()
}

// warcID is a urn:uuid of version 4.
func warcID() string {
	var u [16]byte
	rand.Read(u[:]) //nolint:errcheck // crypto/rand does not fail
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
}
