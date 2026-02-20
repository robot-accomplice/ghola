package bridge

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func newBridgeServer(upstream fasthttp.RequestHandler) (*fasthttputil.InmemoryListener, *fasthttp.Client) {
	upstreamLn := fasthttputil.NewInmemoryListener()
	upstreamSrv := &fasthttp.Server{Handler: upstream}
	go upstreamSrv.Serve(upstreamLn) //nolint:errcheck

	do := func(req *fasthttp.Request, resp *fasthttp.Response, bufferSize int) error {
		c := &fasthttp.Client{
			Dial: func(addr string) (net.Conn, error) { return upstreamLn.Dial() },
		}
		return c.Do(req, resp)
	}

	bridgeLn := fasthttputil.NewInmemoryListener()
	bridgeSrv := &fasthttp.Server{Handler: Handler(do)}
	go bridgeSrv.Serve(bridgeLn) //nolint:errcheck

	bridgeClient := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) { return bridgeLn.Dial() },
	}
	return bridgeLn, bridgeClient
}

func postBridge(t *testing.T, c *fasthttp.Client, br Request) Response {
	t.Helper()
	body, err := json.Marshal(br)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://bridge/")
	req.Header.SetMethod("POST")
	req.SetBody(body)

	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	if err := c.Do(req, rsp); err != nil {
		t.Fatalf("bridge request: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(rsp.Body(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, rsp.Body())
	}
	return resp
}

func TestBridge_BasicGET(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.SetBodyString(`{"result":"ok"}`)
	})
	defer ln.Close()

	resp := postBridge(t, c, Request{
		URL:    "http://upstream",
		Method: "GET",
	})

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Body != `{"result":"ok"}` {
		t.Errorf("body = %q", resp.Body)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestBridge_POST(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Method()) != "POST" {
			t.Errorf("method = %q, want POST", ctx.Method())
		}
		if string(ctx.PostBody()) != `{"jsonrpc":"2.0"}` {
			t.Errorf("body = %q", ctx.PostBody())
		}
		ctx.SetStatusCode(200)
		ctx.SetBodyString("accepted")
	})
	defer ln.Close()

	resp := postBridge(t, c, Request{
		URL:    "http://upstream",
		Method: "POST",
		Body:   `{"jsonrpc":"2.0"}`,
	})

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBridge_CustomHeaders(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Request.Header.Peek("X-Api-Key")) != "secret" {
			t.Error("X-Api-Key header missing or wrong")
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	resp := postBridge(t, c, Request{
		URL:     "http://upstream",
		Method:  "GET",
		Headers: map[string]string{"X-Api-Key": "secret"},
	})

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBridge_GhostSigning(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		ghost := string(ctx.Request.Header.Peek("X-Ghola-Identity"))
		if ghost == "" {
			t.Error("X-Ghola-Identity header missing when ghost=true")
		}
		if len(ghost) != 64 {
			t.Errorf("ghost hash length = %d, want 64", len(ghost))
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	postBridge(t, c, Request{
		URL:   "http://upstream",
		Ghost: true,
	})
}

func TestBridge_NoGhostByDefault(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		if len(ctx.Request.Header.Peek("X-Ghola-Identity")) > 0 {
			t.Error("X-Ghola-Identity should not be set when ghost=false")
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	postBridge(t, c, Request{
		URL:   "http://upstream",
		Ghost: false,
	})
}

func TestBridge_DefaultMethodIsGET(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Method()) != "GET" {
			t.Errorf("method = %q, want GET (default)", ctx.Method())
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	postBridge(t, c, Request{URL: "http://upstream"})
}

func TestBridge_MissingURL(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		t.Fatal("upstream should not be called")
	})
	defer ln.Close()

	resp := postBridge(t, c, Request{})
	if resp.Error == "" {
		t.Error("expected error for missing URL")
	}
}

func TestBridge_HealthEndpoint(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: Handler(nil)}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) { return ln.Dial() },
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://bridge/health")

	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	if err := c.Do(req, rsp); err != nil {
		t.Fatalf("health check: %v", err)
	}
	if rsp.StatusCode() != 200 {
		t.Errorf("health status = %d, want 200", rsp.StatusCode())
	}

	var body map[string]string
	if err := json.Unmarshal(rsp.Body(), &body); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("health status = %q, want ok", body["status"])
	}
}

func TestBridge_InvalidJSON(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: Handler(nil)}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) { return ln.Dial() },
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://bridge/")
	req.Header.SetMethod("POST")
	req.SetBodyString("{not valid json")

	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	if err := c.Do(req, rsp); err != nil {
		t.Fatalf("request: %v", err)
	}
	if rsp.StatusCode() != 400 {
		t.Errorf("status = %d, want 400", rsp.StatusCode())
	}

	var resp Response
	if err := json.Unmarshal(rsp.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected error message for invalid JSON")
	}
}

func TestBridge_UpstreamFailure(t *testing.T) {
	failDoer := func(req *fasthttp.Request, resp *fasthttp.Response, bufferSize int) error {
		return net.ErrClosed
	}

	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: Handler(failDoer)}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) { return ln.Dial() },
	}

	body, _ := json.Marshal(Request{URL: "http://unreachable", Method: "GET"})
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
	if rsp.StatusCode() != 502 {
		t.Errorf("status = %d, want 502", rsp.StatusCode())
	}

	var resp Response
	if err := json.Unmarshal(rsp.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected error message for upstream failure")
	}
}

func TestBridge_DriftEnabled(t *testing.T) {
	ln, c := newBridgeServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	resp := postBridge(t, c, Request{
		URL:   "http://upstream",
		Drift: true,
	})

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBridge_MethodNotAllowed(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: Handler(nil)}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()

	c := &fasthttp.Client{
		Dial: func(addr string) (net.Conn, error) { return ln.Dial() },
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://bridge/")
	req.Header.SetMethod("GET")

	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	if err := c.Do(req, rsp); err != nil {
		t.Fatalf("request: %v", err)
	}
	if rsp.StatusCode() != 405 {
		t.Errorf("status = %d, want 405", rsp.StatusCode())
	}
}
