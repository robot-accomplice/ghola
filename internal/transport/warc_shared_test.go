package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// Every transport of a run writes to one --warc archive: one warcinfo
// record, and a block digest on each record. Before, each transport
// wrote its own warcinfo record, and a run of several URLs gave several.
func TestWARC_TransportsShareOneArchive(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "body") //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	var out bytes.Buffer
	opts := &config.Options{}
	opts.Output.WARCOut = &out
	for range 2 {
		tr, err := New(opts, "")
		if err != nil {
			t.Fatal(err)
		}
		req := fasthttp.AcquireRequest()
		req.SetRequestURI(ts.URL)
		if _, err := tr.Stream(context.Background(), req, io.Discard); err != nil {
			t.Fatal(err)
		}
		fasthttp.ReleaseRequest(req)
	}
	got := out.String()
	if n := strings.Count(got, "WARC-Type: warcinfo\r\n"); n != 1 {
		t.Errorf("%d warcinfo records, want 1", n)
	}
	if r, d := strings.Count(got, "WARC-Type: "), strings.Count(got, "WARC-Block-Digest: sha1:"); r != 5 || d != r {
		t.Errorf("%d records and %d block digests, want 5 of each", r, d)
	}
	if n := strings.Count(got, "WARC-Payload-Digest: sha1:"); n != 2 {
		t.Errorf("%d payload digests, want 1 for each response", n)
	}
}
