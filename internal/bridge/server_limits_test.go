package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
)

// startLimitedBridge runs the bridge server with short limits, as
// ListenAndServe builds it, and returns its address and token.
func startLimitedBridge(t *testing.T, read, idle time.Duration) (string, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, token := ln.Addr().String(), "test-token"
	srv := newServer(Guard(Handler(nil), addr, token), read, idle)
	go srv.Serve(ln)                     //nolint:errcheck
	t.Cleanup(func() { srv.Shutdown() }) //nolint:errcheck
	return addr, token
}

// A client that sends half a request and stops must not hold a
// connection open forever: the read timeout closes it. The timeout covers
// only the client's request, not the upstream fetch.
func TestServer_ReadTimeoutClosesAStalledClient(t *testing.T) {
	addr, _ := startLimitedBridge(t, 200*time.Millisecond, time.Second)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("POST / HTTP/1.1\r\nHost: " + addr + "\r\n")) //nolint:errcheck
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))           //nolint:errcheck
	if _, err := bufio.NewReader(conn).ReadByte(); err == nil {
		// A reply is fine too: the server answered and will close.
		return
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Error("the bridge kept a stalled connection open for 3 s")
	}
}

// A body that is too large gets the reply shape that clients parse:
// status_code, headers, body and error. Before, it was plain text.
func TestServer_TooLargeBodyKeepsTheReplyShape(t *testing.T) {
	addr, token := startLimitedBridge(t, 5*time.Second, 5*time.Second)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Only the header: the server refuses the body from its Content-Length.
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", addr, token, maxBridgeBody+1)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	rsp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	var shape map[string]json.RawMessage
	if err := json.NewDecoder(rsp.Body).Decode(&shape); err != nil {
		t.Fatalf("status %d: the reply is not JSON: %v", rsp.StatusCode, err)
	}
	for _, k := range []string{"status_code", "headers", "body", "error"} {
		if _, ok := shape[k]; !ok {
			t.Errorf("the reply has no %q", k)
		}
	}
}

// A WebSocket session lives longer than the read timeout: the timeout is
// for one HTTP request, not for a connection that the client keeps open.
func TestServer_WebSocketOutlivesTheReadTimeout(t *testing.T) {
	addr, token := startLimitedBridge(t, 200*time.Millisecond, 200*time.Millisecond)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+addr+RPCPath, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(600 * time.Millisecond)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"ghola.list"}`)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	if _, msg, err := conn.ReadMessage(); err != nil || !strings.Contains(string(msg), `"result"`) {
		t.Errorf("after the read timeout: %q, %v", msg, err)
	}
}
