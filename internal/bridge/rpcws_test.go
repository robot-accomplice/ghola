package bridge

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	t.Chdir(t.TempDir()) // RPC jobs write only below the folder where ghola serve runs
	if err := os.Mkdir("out", 0o755); err != nil {
		t.Fatal(err)
	}
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
		"params": map[string]any{"urls": []string{files.URL + "/f"}, "args": []string{"-o", "out/"}}}
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

// The answer to ghola.add comes before the notification of its job, also
// when the download ends at once: a client cannot match a notification
// to a job before it knows the job's ID. CI saw the notification first
// (PR 83: the test read {ID:0 Result:[]} as the answer).
func TestRPC_WebSocketAnswerComesFirst(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer files.Close()
	addr, token := startGuardedBridge(t)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+RPCPath, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck
	dir := "out/"
	for i := range 100 {
		add := map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "ghola.add",
			"params": map[string]any{"urls": []string{fmt.Sprintf("%s/f%d", files.URL, i)}, "args": []string{"-o", dir}}}
		if err := conn.WriteJSON(add); err != nil {
			t.Fatal(err)
		}
		var first struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := conn.ReadJSON(&first); err != nil {
			t.Fatal(err)
		}
		if first.ID != i+1 {
			t.Fatalf("add %d: the first message is %+v, not the answer", i+1, first)
		}
		var note struct {
			Method string `json:"method"`
		}
		if err := conn.ReadJSON(&note); err != nil || note.Method != rpcOnComplete {
			t.Fatalf("add %d: notification %+v, %v", i+1, note, err)
		}
	}
}
