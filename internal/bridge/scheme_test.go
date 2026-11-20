package bridge

import (
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
