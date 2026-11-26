package transport

import (
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // WARC digests are SHA-1 by convention, as in wget
	"encoding/base32"
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

// warcWriters holds one warcWriter for each --warc output, so every
// transport of a run writes to one archive with one warcinfo record, and
// one lock orders their records.
var warcWriters sync.Map // io.Writer -> *warcWriter

// warcFor is the warcWriter of out.
func warcFor(out io.Writer, version string) *warcWriter {
	ww, _ := warcWriters.LoadOrStore(out, &warcWriter{w: out, version: version})
	return ww.(*warcWriter)
}

// warcDigest is a WARC digest value: sha1 in base32, as wget writes it.
func warcDigest(sum []byte) string {
	return "sha1:" + base32.StdEncoding.EncodeToString(sum)
}

// blockDigest is the WARC-Block-Digest line of a block in memory.
func blockDigest(block []byte) string {
	sum := sha1.Sum(block) //nolint:gosec
	return "WARC-Block-Digest: " + warcDigest(sum[:]) + "\r\n"
}

// responseDigests are the block and payload digest lines of a response:
// the block is the head and the body, the payload is the body. It reads
// the body once and puts it back at the start.
func responseDigests(head []byte, body io.ReadSeeker) (string, error) {
	blockSum, payloadSum := sha1.New(), sha1.New() //nolint:gosec
	blockSum.Write(head)                           //nolint:errcheck // a hash does not fail
	if _, err := io.Copy(io.MultiWriter(blockSum, payloadSum), body); err != nil {
		return "", fmt.Errorf("--warc: %w", err)
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("--warc: %w", err)
	}
	return "WARC-Block-Digest: " + warcDigest(blockSum.Sum(nil)) + "\r\n" +
		"WARC-Payload-Digest: " + warcDigest(payloadSum.Sum(nil)) + "\r\n", nil
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
	head := meta.HeaderBlock()
	digests, err := responseDigests(head, body)
	if err != nil {
		return err
	}
	ww.mu.Lock()
	defer ww.mu.Unlock()
	if !ww.started {
		fields := "software: ghola/" + ww.version + "\r\nformat: WARC File Format 1.1\r\n"
		if err := ww.write("warcinfo", "", "application/warc-fields", "", blockDigest([]byte(fields)),
			strings.NewReader(fields), int64(len(fields))); err != nil {
			return err
		}
		ww.started = true
	}
	target := string(req.URI().FullURI())
	respID := warcID()
	if err := ww.write("response", target, "application/http;msgtype=response", respID, digests,
		io.MultiReader(strings.NewReader(string(head)), body), int64(len(head))+size); err != nil {
		return err
	}
	reqBlock := append([]byte(requestHead(req)), req.Body()...)
	return ww.write("request", target, "application/http;msgtype=request", "<"+respID+">", blockDigest(reqBlock),
		strings.NewReader(string(reqBlock)), int64(len(reqBlock)))
}

// write writes one record. For a response, id is the record's own ID;
// for another record, it is the response it belongs to, or "". digests
// are the digest header lines of the block.
func (ww *warcWriter) write(kind, target, contentType, id, digests string, block io.Reader, length int64) error {
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
	h.WriteString(digests)
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
