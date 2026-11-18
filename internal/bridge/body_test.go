package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// call sends br to a bridge whose upstream is doer, and returns the status
// and the decoded reply.
func call(t *testing.T, doer func(context.Context, *config.Options, *fasthttp.Request, *fasthttp.Response) error, br Request) (int, Response) {
	t.Helper()
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: Handler(doer)}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()
	c := &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}

	body, _ := json.Marshal(br)
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://bridge/")
	req.Header.SetMethod("POST")
	req.SetBody(body)
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	if err := c.Do(req, rsp); err != nil {
		t.Fatalf("request: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(rsp.Body(), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", rsp.Body(), err)
	}
	return rsp.StatusCode(), resp
}

// A body that breaks off is a 502 with the error. The body is now a stream:
// rsp.Body() on it would put the error text in the JSON "body" field, and
// the bridge would answer 200.
func TestBridge_BrokenBodyIsBadGateway(t *testing.T) {
	brokenDoer := func(ctx context.Context, opts *config.Options, req *fasthttp.Request, resp *fasthttp.Response) error {
		resp.SetStatusCode(200)
		resp.SetBodyStream(io.MultiReader(bytes.NewReader([]byte("part")), errReader{errors.New("connection reset")}), -1)
		return nil
	}
	status, resp := call(t, brokenDoer, Request{URL: "http://upstream", Method: "GET"})
	if status != 502 {
		t.Errorf("status = %d, want 502", status)
	}
	if !strings.Contains(resp.Error, "connection reset") {
		t.Errorf("error = %q, want the read error", resp.Error)
	}
}

// "retries": -1 is an error reply. Before the fix FetchURL returned no
// response and no error, and the bridge used the nil response (audit
// finding H5).
func TestBridge_NegativeRetriesIsAnError(t *testing.T) {
	doer := func(context.Context, *config.Options, *fasthttp.Request, *fasthttp.Response) error {
		t.Error("no request may be sent")
		return nil
	}
	status, resp := call(t, doer, Request{URL: "http://upstream", Method: "GET", Retries: -1})
	if status != 502 || !strings.Contains(resp.Error, "negative") {
		t.Errorf("status = %d, error = %q; want 502 and the retries error", status, resp.Error)
	}
}
