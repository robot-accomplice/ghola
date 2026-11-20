package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b) //nolint:errcheck
	w.Close()
	return buf.Bytes()
}

func downloadOnce(t *testing.T, h http.HandlerFunc) []byte {
	t.Helper()
	ts := httptest.NewServer(h)
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	opts := &config.Options{URL: ts.URL, Stealth: config.StealthOptions{Compressed: true},
		Output: config.OutputOptions{File: file, Silent: true}}
	if err := Download(context.Background(), opts, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(file)
	return got
}

// A .gz file that the server sends as it is (no Content-Encoding) is the
// file the user asked for. --compressed decodes only a content coding, so
// the download must keep the gzip bytes.
func TestDownload_CompressedKeepsAGzipFile(t *testing.T) {
	archive := gz(t, bytes.Repeat([]byte("archive "), 1000))
	got := downloadOnce(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(archive) //nolint:errcheck
	})
	if !bytes.Equal(got, archive) {
		t.Fatalf("the file is %d bytes, not the %d-byte .gz file: ghola decoded it", len(got), len(archive))
	}
}

// A download with a br or zstd Content-Encoding is decoded into the file.
func TestDownload_CompressedDecodesBrAndZstd(t *testing.T) {
	want := bytes.Repeat([]byte("decoded "), 2000)
	for _, ce := range []string{"br", "zstd", "gzip"} {
		enc := encodeAs(t, ce, want)
		got := downloadOnce(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept-Encoding") != "gzip, deflate, br, zstd" {
				t.Errorf("Accept-Encoding = %q", r.Header.Get("Accept-Encoding"))
			}
			w.Header().Set("Content-Encoding", ce)
			w.Write(enc) //nolint:errcheck
		})
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the file is %d bytes, want %d decoded bytes", ce, len(got), len(want))
		}
	}
}

// A coding that ghola cannot decode stops the download before the file
// opens (curl exit 61), so no encoded bytes are left as the file.
func TestDownload_UnknownEncodingWritesNoFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "compress")
		w.Write([]byte("x")) //nolint:errcheck
	}))
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	opts := &config.Options{URL: ts.URL, Stealth: config.StealthOptions{Compressed: true},
		Output: config.OutputOptions{File: file, Silent: true}}
	if err := Download(context.Background(), opts, nil); !errors.Is(err, gholatransport.ErrBadEncoding) {
		t.Fatalf("err = %v, want ErrBadEncoding", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("a file was written: %v", err)
	}
}

func encodeAs(t *testing.T, ce string, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch ce {
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		w, _ = zstd.NewWriter(&buf)
	default:
		return gz(t, b)
	}
	w.Write(b) //nolint:errcheck
	w.Close()
	return buf.Bytes()
}
