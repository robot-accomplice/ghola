package mirror

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // WARC digests are SHA-1 by convention
	"encoding/base32"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A mirror with --warc writes one archive of the whole run: one warcinfo
// record, a response record for each exchange, robots.txt included, and
// the digests that tools use to check and to deduplicate the records.
func TestMirror_WARCRecordsTheWholeRunWithDigests(t *testing.T) {
	ts, _ := site(t)
	var warc bytes.Buffer
	base := baseOptions(t, ts.URL+"/")
	base.Output.WARCOut = &warc
	m := &Mirror{Base: base, Root: t.TempDir(),
		Opts: config.MirrorOptions{On: true, Depth: 3, Robots: true, Requisites: true, Layout: "no-host"}}
	m.Run(context.Background()) //nolint:errcheck // /missing.html fails the run
	out := warc.String()
	if n := strings.Count(out, "WARC-Type: warcinfo\r\n"); n != 1 {
		t.Errorf("%d warcinfo records, want 1 for the run", n)
	}
	if !strings.Contains(out, "WARC-Target-URI: "+ts.URL+"/robots.txt\r\n") {
		t.Error("robots.txt is not in the archive")
	}
	body := "<html><body><a href=\"/\">home</a><a href=\"/missing.html\">m</a></body></html>"
	sum := sha1.Sum([]byte(body)) //nolint:gosec
	if want := "WARC-Payload-Digest: sha1:" + base32.StdEncoding.EncodeToString(sum[:]) + "\r\n"; !strings.Contains(out, want) {
		t.Errorf("no %q for /a.html", want)
	}
	if r, b := strings.Count(out, "WARC-Type: "), strings.Count(out, "WARC-Block-Digest: sha1:"); r != b {
		t.Errorf("%d records, %d block digests: each record has one", r, b)
	}
}
