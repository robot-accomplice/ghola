package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/websocket"
)

// wsServer echoes: it sends "hello ", then "echo:<msg>" for a first message
// that the client sends within the session, else "bye", and closes. It
// records the handshake headers.
type wsServer struct {
	mu     sync.Mutex
	header http.Header
}

// handler accepts any handshake: x/net/websocket.Handler refuses one
// without Origin, and a non-browser client (curl too) sends none.
func (s *wsServer) handler(expectMessage bool) http.Handler {
	return websocket.Server{Handler: func(ws *websocket.Conn) {
		s.mu.Lock()
		s.header = ws.Request().Header.Clone()
		s.mu.Unlock()
		websocket.Message.Send(ws, "hello ") //nolint:errcheck
		if expectMessage {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err == nil {
				websocket.Message.Send(ws, "echo:"+msg) //nolint:errcheck
			}
		} else {
			websocket.Message.Send(ws, "bye") //nolint:errcheck
		}
		ws.Close()
	}}
}

func wsGet(t *testing.T, opts *config.Options, proxy, url, data string, hdr map[string]string) (string, error) {
	t.Helper()
	tr, err := New(opts, proxy)
	if err != nil {
		t.Fatal(err)
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(url)
	if data != "" {
		req.Header.SetMethod("POST")
		req.SetBodyString(data)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	var out strings.Builder
	_, err = tr.Stream(context.Background(), req, &out)
	return out.String(), err
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

// ghola writes the messages that the server sends until it closes, on both
// stacks, over ws:// and wss://. The handshake carries the -H headers.
func TestWebSocket_ReceivesMessages(t *testing.T) {
	for _, imp := range stacks {
		for _, secure := range []bool{false, true} {
			t.Run("impersonate="+imp+"/tls="+map[bool]string{false: "no", true: "yes"}[secure], func(t *testing.T) {
				s := &wsServer{}
				var ts *httptest.Server
				if secure {
					ts = httptest.NewTLSServer(s.handler(false))
				} else {
					ts = httptest.NewServer(s.handler(false))
				}
				defer ts.Close()
				opts := &config.Options{Stealth: config.StealthOptions{Impersonate: imp, Insecure: true}}
				got, err := wsGet(t, opts, "", wsURL(ts.URL)+"/feed", "", map[string]string{"X-Probe": "1"})
				if err != nil || got != "hello bye" {
					t.Fatalf("got %q, err %v", got, err)
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				if s.header.Get("X-Probe") != "1" {
					t.Errorf("the handshake lacks X-Probe: %v", s.header)
				}
			})
		}
	}
}

// -d sends one text message after the handshake.
func TestWebSocket_SendsData(t *testing.T) {
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			s := &wsServer{}
			ts := httptest.NewServer(s.handler(true))
			defer ts.Close()
			got, err := wsGet(t, &config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, "", wsURL(ts.URL), "ping", nil)
			if err != nil || got != "hello echo:ping" {
				t.Fatalf("got %q, err %v", got, err)
			}
		})
	}
}

// A WebSocket goes through a SOCKS proxy, and through an HTTP proxy with
// CONNECT.
func TestWebSocket_Proxies(t *testing.T) {
	s := &wsServer{}
	ts := httptest.NewServer(s.handler(false))
	defer ts.Close()
	socks := newSocksServer(t, "")
	if got, err := wsGet(t, &config.Options{}, "socks5h://"+socks.ln.Addr().String(), wsURL(ts.URL), "", nil); err != nil || got != "hello bye" {
		t.Fatalf("socks: %q, %v", got, err)
	}
	if v, _, _ := socks.last(); v != 5 {
		t.Error("the WebSocket did not go through the SOCKS proxy")
	}
	var tunnels int
	var mu sync.Mutex
	targetAddr := ts.Listener.Addr().String()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		tunnels++
		mu.Unlock()
		up, err := (&dialerForTest{}).dial(targetAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")) //nolint:errcheck
		go io.Copy(up, c)                          //nolint:errcheck
		io.Copy(c, up)                             //nolint:errcheck
		c.Close()
		up.Close()
	}))
	defer proxy.Close()
	if got, err := wsGet(t, &config.Options{}, proxy.URL, wsURL(ts.URL), "", nil); err != nil || got != "hello bye" {
		t.Fatalf("http proxy: %q, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if tunnels != 1 {
		t.Errorf("%d CONNECT tunnels, want 1", tunnels)
	}
}

type dialerForTest struct{}

func (dialerForTest) dial(addr string) (io.ReadWriteCloser, error) {
	return net.Dial("tcp", addr)
}

// A session that the server keeps open ends at --max-time with the
// deadline error (exit 28), not with a closed-connection error.
func TestWebSocket_MaxTimeIsATimeout(t *testing.T) {
	ts := httptest.NewServer(websocket.Server{Handler: func(ws *websocket.Conn) {
		websocket.Message.Send(ws, "first") //nolint:errcheck
		var never string
		websocket.Message.Receive(ws, &never) //nolint:errcheck
	}})
	defer ts.Close()
	tr, _ := New(&config.Options{}, "")
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(wsURL(ts.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var out strings.Builder
	if _, err := tr.Stream(ctx, req, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if out.String() != "first" {
		t.Errorf("got %q", out.String())
	}
}
