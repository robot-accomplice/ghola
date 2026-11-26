package bridge

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

// A bridge client sends URLs from outside data. The bridge must not read a
// local file for it, now that ghola reads file:// URLs.
func TestBridge_RefusesAFileURL(t *testing.T) {
	reached := false
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) { reached = true })
	defer ln.Close()
	for _, u := range []string{"file:///etc/passwd", "ftp://host/f", "gopher://host/"} {
		resp := postBridge(t, c, Request{URL: u, Method: "GET"})
		if resp.Error == "" {
			t.Errorf("%s: no error", u)
		}
	}
	if reached {
		t.Error("a request reached the upstream")
	}
}

// The bridge fetches http(s) for its clients, and a redirect must not take
// it to another protocol either: a server could send the bridge to an FTP
// host and the reply would carry that file. ghola alone follows ftp://
// redirects, as curl does; the bridge does not.
func TestBridge_FollowsOnlyHTTPRedirects(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		ctx.Redirect("ftp://127.0.0.1:1/file.bin", fasthttp.StatusFound)
	})
	defer ln.Close()
	resp := postBridge(t, c, Request{URL: "http://upstream/start", Method: "GET", Location: true})
	if !strings.Contains(resp.Error, "redirect to a protocol") {
		t.Errorf("error %q, want the refused redirect", resp.Error)
	}
}
