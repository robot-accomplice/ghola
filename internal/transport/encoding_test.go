package transport

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type encoder func(io.Writer) io.WriteCloser

var encoders = map[string]encoder{
	"gzip":    func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) },
	"deflate": func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) },
	"br":      func(w io.Writer) io.WriteCloser { return brotli.NewWriter(w) },
	"zstd": func(w io.Writer) io.WriteCloser {
		z, _ := zstd.NewWriter(w)
		return z
	},
}

func encode(t *testing.T, b []byte, codings ...string) []byte {
	t.Helper()
	for _, c := range codings {
		var buf bytes.Buffer
		w := encoders[c](&buf)
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
		w.Close()
		b = buf.Bytes()
	}
	return b
}

func decodeAll(t *testing.T, b []byte, ce string) ([]byte, error) {
	t.Helper()
	r, err := DecodeBody(bytes.NewReader(b), ce)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

var plain = bytes.Repeat([]byte("ghola decodes this. "), 500)

// Each coding that --compressed asks for is removed, and a list of codings
// is removed last first, as RFC 9110 orders Content-Encoding.
func TestDecodeBody_EachCoding(t *testing.T) {
	cases := map[string][]string{
		"gzip": {"gzip"}, "x-gzip": {"gzip"}, "deflate": {"deflate"}, "br": {"br"}, "zstd": {"zstd"},
		"gzip, br": {"gzip", "br"}, "identity": nil, "": nil,
	}
	for ce, applied := range cases {
		got, err := decodeAll(t, encode(t, plain, applied...), ce)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("Content-Encoding %q: %d bytes, err %v", ce, len(got), err)
		}
	}
}

// Some servers send raw deflate for "deflate". It decodes too.
func TestDecodeBody_RawDeflate(t *testing.T) {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	w.Write(plain) //nolint:errcheck
	w.Close()
	if got, err := decodeAll(t, buf.Bytes(), "deflate"); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("%d bytes, err %v", len(got), err)
	}
}

// A coding that ghola cannot decode is an error (curl exit 61), before any
// byte is read.
func TestDecodeBody_UnknownCoding(t *testing.T) {
	if _, err := DecodeBody(bytes.NewReader(plain), "compress"); !errors.Is(err, ErrBadEncoding) {
		t.Fatalf("err = %v, want ErrBadEncoding", err)
	}
}

// A zstd frame that needs a window above 8 MB (RFC 9659) is refused, so a
// small body cannot make the decoder take a large buffer.
func TestDecodeBody_ZstdWindowLimit(t *testing.T) {
	var buf bytes.Buffer
	z, _ := zstd.NewWriter(&buf, zstd.WithWindowSize(64<<20), zstd.WithSingleSegment(false))
	z.Write(bytes.Repeat([]byte{1}, 32<<20)) //nolint:errcheck
	z.Close()
	if _, err := decodeAll(t, buf.Bytes(), "zstd"); err == nil {
		t.Fatal("a 64 MB window decoded")
	}
}
