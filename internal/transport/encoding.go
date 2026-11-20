package transport

import (
	"bufio"
	"compress/flate"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// AcceptEncoding is what --compressed asks for: every coding that
// DecodeBody can remove (v1 defaults, item 7).
const AcceptEncoding = "gzip, deflate, br, zstd"

// zstdMaxWindow is the largest zstd window that ghola decodes. RFC 9659
// limits a zstd content coding to an 8 MB window, so a body cannot make the
// decoder take more memory than that.
const zstdMaxWindow = 8 << 20

// ErrBadEncoding marks a Content-Encoding that ghola cannot decode. curl
// stops with "Unrecognized content encoding type" (exit 61).
var ErrBadEncoding = errors.New("unrecognized content encoding")

// codings returns the content codings of a Content-Encoding value in the
// order to remove them: the last one applied comes first. identity is
// dropped.
func codings(contentEncoding string) []string {
	var out []string
	for _, c := range strings.Split(contentEncoding, ",") {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" && c != "identity" {
			out = append([]string{c}, out...)
		}
	}
	return out
}

// CheckEncoding returns ErrBadEncoding when DecodeBody cannot remove each
// coding of contentEncoding. A download calls it before the file opens.
func CheckEncoding(contentEncoding string) error {
	for _, c := range codings(contentEncoding) {
		switch c {
		case "gzip", "x-gzip", "deflate", "br", "zstd":
		default:
			return fmt.Errorf("%w: %q", ErrBadEncoding, c)
		}
	}
	return nil
}

// DecodeBody returns a reader that removes the content codings of
// contentEncoding from r. The response header decides, never the body
// bytes: a .gz file sent with no Content-Encoding is the file itself, and
// it stays as it is.
func DecodeBody(r io.Reader, contentEncoding string) (io.Reader, error) {
	if err := CheckEncoding(contentEncoding); err != nil {
		return nil, err
	}
	for _, c := range codings(contentEncoding) {
		var err error
		if r, err = decodeOne(r, c); err != nil {
			return nil, fmt.Errorf("decode %s body: %w", c, err)
		}
	}
	return r, nil
}

func decodeOne(r io.Reader, coding string) (io.Reader, error) {
	switch coding {
	case "gzip", "x-gzip":
		// A server that says gzip and sends plain bytes is common; those
		// bytes pass through unchanged, as before.
		return DecodeGzip(r)
	case "deflate":
		return decodeDeflate(r)
	case "br":
		return brotli.NewReader(r), nil
	default: // zstd: CheckEncoding allows nothing else
		d, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(zstdMaxWindow))
		if err != nil {
			return nil, err
		}
		return d.IOReadCloser(), nil
	}
}

// decodeDeflate reads "deflate" as RFC 9110 says (zlib), and also accepts
// raw deflate, which some servers send.
func decodeDeflate(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	if h, _ := br.Peek(2); len(h) == 2 && h[0]&0x0f == 8 && (uint16(h[0])<<8|uint16(h[1]))%31 == 0 {
		return zlib.NewReader(br)
	}
	return flate.NewReader(br), nil
}
