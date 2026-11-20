package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// warcRecord is one record of a WARC file.
type warcRecord struct {
	headers map[string]string
	block   []byte
}

// readWARC reads the records, and fails when a Content-Length does not
// match its block: a WARC reader would then lose every record after it.
func readWARC(t *testing.T, path string) []warcRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var out []warcRecord
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			return out
		}
		if strings.TrimSpace(line) != "WARC/1.1" {
			t.Fatalf("record %d starts with %q", len(out), line)
		}
		rec := warcRecord{headers: map[string]string{}}
		for {
			line, _ = r.ReadString('\n')
			if line == "\r\n" {
				break
			}
			k, v, _ := strings.Cut(strings.TrimSpace(line), ": ")
			rec.headers[k] = v
		}
		n, _ := strconv.Atoi(rec.headers["Content-Length"])
		rec.block = make([]byte, n)
		if _, err := io.ReadFull(r, rec.block); err != nil {
			t.Fatal(err)
		}
		end := make([]byte, 4)
		if _, err := io.ReadFull(r, end); err != nil || string(end) != "\r\n\r\n" {
			t.Fatalf("record %d: the block is not %d bytes", len(out), n)
		}
		out = append(out, rec)
	}
}

// --warc writes a warcinfo record, then the response and the request of
// each exchange, as wget --warc-file does; the response holds the bytes
// as they came. The stdout path cannot record, so it is an error.
func TestExecute_WARC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Mark", "warc")
		fmt.Fprint(w, "the body")
	}))
	defer srv.Close()
	dir := t.TempDir()
	warc := filepath.Join(dir, "out.warc")
	if code := runGhola(t, "download", "-q", "--warc", warc, "-o", filepath.Join(dir, "f"), srv.URL+"/x"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	recs := readWARC(t, warc)
	var kinds []string
	for _, r := range recs {
		kinds = append(kinds, r.headers["WARC-Type"])
	}
	if strings.Join(kinds, ",") != "warcinfo,response,request" {
		t.Fatalf("records: %v", kinds)
	}
	resp, req := recs[1], recs[2]
	if !bytes.HasPrefix(resp.block, []byte("HTTP/1.1 200")) || !bytes.HasSuffix(resp.block, []byte("\r\n\r\nthe body")) || !bytes.Contains(resp.block, []byte("X-Mark: warc")) {
		t.Errorf("response block:\n%s", resp.block)
	}
	if !bytes.HasPrefix(req.block, []byte("GET /x HTTP/1.1")) || resp.headers["WARC-Target-URI"] != srv.URL+"/x" {
		t.Errorf("request block %q, target %q", req.block, resp.headers["WARC-Target-URI"])
	}
	if req.headers["WARC-Concurrent-To"] != resp.headers["WARC-Record-ID"] {
		t.Error("the request record does not point to its response")
	}
	if code := runGhola(t, "-q", "--warc", filepath.Join(dir, "x.warc"), srv.URL); code != 2 {
		t.Errorf("--warc on stdout: exit %d, want 2", code)
	}
}
