package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/valyala/fasthttp"
)

// cookie_jar names a file that the bridge writes. A client may name one
// only below the folder where ghola serve runs, so it cannot replace a
// file anywhere else. The refusal keeps the reply shape that clients
// parse: status_code, headers, body and error.
func TestBridge_CookieJarStaysInTheServeFolder(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) { ctx.SetBodyString("ok") })
	defer ln.Close()
	outside := filepath.Join(filepath.Dir(root), "jar-outside.txt")
	for _, jar := range []string{outside, "../jar-up.txt"} {
		raw := postBridgeRaw(t, c, Request{URL: "http://upstream/x", Method: "GET", CookieJar: jar})
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(raw, &shape); err != nil {
			t.Fatalf("%s: %v: %s", jar, err, raw)
		}
		for _, k := range []string{"status_code", "headers", "body", "error"} {
			if _, ok := shape[k]; !ok {
				t.Errorf("%s: the reply has no %q: %s", jar, k, raw)
			}
		}
		if string(shape["error"]) == "" || string(shape["error"]) == `""` {
			t.Errorf("%s: no error: %s", jar, raw)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("the bridge wrote a cookie jar outside the serve folder")
	}
	if resp := postBridge(t, c, Request{URL: "http://upstream/x", Method: "GET", CookieJar: "jar.txt"}); resp.Error != "" {
		t.Errorf("a jar inside the folder: %s", resp.Error)
	}
}

// postBridgeRaw returns the raw reply body of POST /.
func postBridgeRaw(t *testing.T, c *fasthttp.Client, br Request) []byte {
	t.Helper()
	body, _ := json.Marshal(br)
	req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)
	req.SetRequestURI("http://bridge/")
	req.Header.SetMethod("POST")
	req.SetBody(body)
	if err := c.Do(req, rsp); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), rsp.Body()...)
}
