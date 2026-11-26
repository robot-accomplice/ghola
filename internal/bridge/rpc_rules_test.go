package bridge

import (
	"encoding/json"
	"testing"

	"github.com/valyala/fasthttp"
)

// rpcRaw posts body to the RPC endpoint and returns the status and reply.
func rpcRaw(h fasthttp.RequestHandler, body string) (int, []byte) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodPost)
	ctx.Request.SetRequestURI(RPCPath)
	ctx.Request.SetBodyString(body)
	h(ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

// JSON-RPC 2.0: a batch (an array) is a valid message that this server does
// not take, so the error is Invalid Request (-32600), not Parse error. A
// notification (no id) gets no reply. An id must be a string, a number or
// null.
func TestRPC_JSONRPCRules(t *testing.T) {
	t.Chdir(t.TempDir())
	h := Handler(nil)
	code := func(body []byte) int {
		var r struct {
			Error *rpcError `json:"error"`
		}
		json.Unmarshal(body, &r) //nolint:errcheck
		if r.Error == nil {
			return 0
		}
		return r.Error.Code
	}
	if _, b := rpcRaw(h, `[{"jsonrpc":"2.0","id":1,"method":"ghola.list"}]`); code(b) != rpcInvalidRequest {
		t.Errorf("batch: %s, want error %d", b, rpcInvalidRequest)
	}
	if st, b := rpcRaw(h, `{"jsonrpc":"2.0","method":"ghola.list"}`); len(b) != 0 || st != fasthttp.StatusNoContent {
		t.Errorf("notification: status %d, reply %q; want 204 and no reply", st, b)
	}
	if _, b := rpcRaw(h, `{"jsonrpc":"2.0","id":{"a":1},"method":"ghola.list"}`); code(b) != rpcInvalidRequest {
		t.Errorf("object id: %s, want error %d", b, rpcInvalidRequest)
	}
	if _, b := rpcRaw(h, `{"jsonrpc":"2.0","id":"x","method":"ghola.list"}`); code(b) != 0 {
		t.Errorf("string id: %s", b)
	}
}

// A job that is complete or removed cannot pause, and a removed job cannot
// stop again: the answer is an error, as in aria2. Before, pause set any job
// to paused, and resume then ran a finished job again.
func TestRPC_JobStateMoves(t *testing.T) {
	t.Chdir(t.TempDir())
	s := newRPC(nil)
	s.jobs["done"] = &rpcJob{GID: "done", Status: "complete"}
	s.jobs["gone"] = &rpcJob{GID: "gone", Status: "removed"}
	for gid, stop := range map[string]bool{"done": false, "gone": false} {
		if _, rerr := s.halt(gid, stop); rerr == nil {
			t.Errorf("pause %s: no error", gid)
		}
	}
	if _, rerr := s.halt("gone", true); rerr == nil {
		t.Error("stop a removed job: no error")
	}
	if j := s.jobs["done"]; j.Status != "complete" {
		t.Errorf("the complete job is now %s", j.Status)
	}
}

// A repeated response header keeps every value in the reply map, joined
// with ", " as HTTP allows. Set-Cookie is not joined: its values hold
// commas (in Expires), so the map keeps the last one, as before. Before,
// the map kept only the last value of every header.
func TestBridge_RepeatedHeadersKeepEveryValue(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Add("X-Multi", "a")
		ctx.Response.Header.Add("X-Multi", "b")
		ctx.Response.Header.Add("Set-Cookie", "c1=1")
		ctx.Response.Header.Add("Set-Cookie", "c2=2")
	})
	defer ln.Close()
	resp := postBridge(t, c, Request{URL: "http://upstream/x", Method: "GET"})
	if got := resp.Headers["X-Multi"]; got != "a, b" {
		t.Errorf("X-Multi = %q, want %q", got, "a, b")
	}
	if got := resp.Headers["Set-Cookie"]; got != "c2=2" {
		t.Errorf("Set-Cookie = %q, want the last value", got)
	}
}
