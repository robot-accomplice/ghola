package bridge

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/valyala/fasthttp"
)

// startGuardedBridge runs the bridge with its checks on a real listener,
// as ghola serve does. It returns the address and the token.
func startGuardedBridge(t *testing.T) (string, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, token := ln.Addr().String(), "test-token"
	srv := &fasthttp.Server{Handler: Guard(Handler(nil), addr, token)}
	go srv.Serve(ln)                     //nolint:errcheck
	t.Cleanup(func() { srv.Shutdown() }) //nolint:errcheck
	return addr, token
}

// The JSON-RPC endpoint takes WebSocket too (spec decision M): a client
// sends requests on one connection and gets ghola.onComplete when a job
// ends, as aria2's onDownloadComplete.
func TestRPC_WebSocket(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "body") }))
	defer files.Close()
	addr, token := startGuardedBridge(t)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+RPCPath, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	add := map[string]any{"jsonrpc": "2.0", "id": 7, "method": "ghola.add",
		"params": map[string]any{"urls": []string{files.URL + "/f"}, "args": []string{"-o", t.TempDir() + "/"}}}
	if err := conn.WriteJSON(add); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		ID     int      `json:"id"`
		Result []string `json:"result"`
	}
	if err := conn.ReadJSON(&answer); err != nil || answer.ID != 7 || len(answer.Result) != 1 {
		t.Fatalf("answer %+v, %v", answer, err)
	}
	var note struct {
		Method string              `json:"method"`
		Params []map[string]string `json:"params"`
	}
	if err := conn.ReadJSON(&note); err != nil {
		t.Fatal(err)
	}
	if note.Method != rpcOnComplete || len(note.Params) != 1 || note.Params[0]["gid"] != answer.Result[0] {
		t.Errorf("notification %+v, want %s for %s", note, rpcOnComplete, answer.Result[0])
	}
}

// A WebSocket needs the token, as a POST does. A browser cannot send
// Authorization on a WebSocket, so a web page cannot reach the RPC. A
// request with the Origin of another site is refused too.
func TestRPC_WebSocketChecks(t *testing.T) {
	addr, token := startGuardedBridge(t)
	for name, h := range map[string]http.Header{
		"no token":       {},
		"wrong token":    {"Authorization": {"Bearer nope"}},
		"foreign Origin": {"Authorization": {"Bearer " + token}, "Origin": {"https://evil.example"}},
	} {
		conn, rsp, err := websocket.DefaultDialer.Dial("ws://"+addr+RPCPath, h)
		if err == nil {
			conn.Close()
			t.Errorf("%s: the connection opened", name)
			continue
		}
		if rsp == nil || rsp.StatusCode < 400 {
			t.Errorf("%s: %v, want a 4xx refusal", name, err)
		}
	}
	// The Host check still applies: a rebound name is refused.
	d := websocket.Dialer{NetDial: func(string, string) (net.Conn, error) { return net.Dial("tcp", addr) }}
	if conn, _, err := d.Dial("ws://rebound.example"+RPCPath, http.Header{"Authorization": {"Bearer " + token}}); err == nil {
		conn.Close()
		t.Error("a foreign Host opened the connection")
	}
}
