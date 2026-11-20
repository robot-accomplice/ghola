package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/websocket"
	"github.com/valyala/fasthttp"
)

// The WebSocket schemes (plan phase 5, decision D1).
const (
	SchemeWS  = "ws"
	SchemeWSS = "wss"
)

// ErrProxyConnect marks an HTTP proxy that refused the CONNECT tunnel.
var ErrProxyConnect = errors.New("the proxy refused the tunnel")

// serveWebSocket connects to a ws:// or wss:// URL and answers with the
// messages that the server sends, as one body, until the server closes:
// curl writes the received messages to stdout in the same way. -d sends
// one text message after the handshake, and -T one binary message.
//
// The handshake carries the request headers (-H, -A, cookies, -u, the
// profile headers). With --impersonate, tls-client's own dialers make the
// connection, so the TLS handshake has the browser fingerprint and the
// proxy settings of that stack.
func (t protocolTransport) serveWebSocket(ctx context.Context, req *fasthttp.Request) (fileAnswer, error) {
	u, err := requestURL(req)
	if err != nil {
		return fileAnswer{}, err
	}
	d, err := t.wsDialer(u)
	if err != nil {
		return fileAnswer{}, err
	}
	header := fhttp.Header{}
	for k, v := range req.Header.All() {
		switch strings.ToLower(string(k)) {
		case "host", "connection", "upgrade", "content-length", "content-type",
			"sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions":
			continue // the WebSocket handshake sets these
		}
		header.Add(string(k), string(v))
	}
	conn, rsp, err := d.DialContext(ctx, u.String(), header)
	if rsp != nil && rsp.Body != nil {
		rsp.Body.Close()
	}
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) && rsp != nil {
			return fileAnswer{}, fmt.Errorf("websocket handshake: the server answered %d", rsp.StatusCode)
		}
		return fileAnswer{}, err
	}
	if body := req.Body(); len(body) > 0 {
		kind := websocket.TextMessage
		if string(req.Header.Method()) == fasthttp.MethodPut { // -T sends a file
			kind = websocket.BinaryMessage
		}
		if err := conn.WriteMessage(kind, body); err != nil {
			conn.Close()
			return fileAnswer{}, err
		}
	}
	pr, pw := io.Pipe()
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				switch {
				case ctx.Err() != nil:
					err = ctx.Err() // --max-time (exit 28) or Ctrl-C closed the connection
				case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived):
					err = nil // a close frame ends the body
				}
				pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(msg); err != nil {
				return
			}
		}
	}()
	go func() { <-ctx.Done(); conn.Close() }() // --max-time and Ctrl-C end the session
	return fileAnswer{
		status: http.StatusOK, header: http.Header{}, length: -1,
		body: closer{Reader: pr, close: func() error { pr.Close(); return conn.Close() }},
	}, nil
}

// wsDialer returns the WebSocket dialer for the stack.
func (t protocolTransport) wsDialer(u *url.URL) (*websocket.Dialer, error) {
	if tc, ok := t.network.(*tlsClientTransport); ok {
		c := tc.client
		if tc.envProxy {
			// Pick the client for this target's environment proxy, as an
			// HTTP request does.
			target := *u
			target.Scheme = httpSchemeOf(u.Scheme)
			req, err := fhttp.NewRequestWithContext(withTarget(context.Background(), &target), "GET", target.String(), nil)
			if err != nil {
				return nil, err
			}
			if c, err = tc.clientFor(req); err != nil {
				return nil, err
			}
		}
		return &websocket.Dialer{NetDialContext: c.GetDialer().DialContext, NetDialTLSContext: c.GetTLSDialer()}, nil
	}
	base, err := t.wsTunnel(u)
	if err != nil {
		return nil, err
	}
	cfg, err := buildTLSConfig(t.opts)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg = cfg.Clone()
	cfg.ServerName = u.Hostname()
	cfg.NextProtos = []string{"http/1.1"} // the WebSocket upgrade is HTTP/1.1
	return &websocket.Dialer{
		NetDialContext: base,
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := base(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			tc := tls.Client(conn, cfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return tc, nil
		},
	}, nil
}

// wsTunnel returns the dial function of the net/http stack for a WebSocket:
// direct, through a SOCKS proxy, or through an HTTP proxy with CONNECT.
func (t protocolTransport) wsTunnel(u *url.URL) (dialFunc, error) {
	direct := (&net.Dialer{}).DialContext
	target := *u
	target.Scheme = httpSchemeOf(u.Scheme) // ws uses http_proxy, wss https_proxy, as in curl
	p, err := t.proxyFor(&target)
	if err != nil || p == nil {
		return direct, err
	}
	if isSOCKS(p) {
		return socksDialer(p, direct), nil
	}
	return connectDialer(p, direct), nil
}

// proxyFor returns --proxy, else the environment proxy for target.
func (t protocolTransport) proxyFor(target *url.URL) (*url.URL, error) {
	if t.proxy != "" {
		return url.Parse(t.proxy)
	}
	return proxyFromEnv(target)
}

func httpSchemeOf(wsScheme string) string {
	if strings.EqualFold(wsScheme, SchemeWSS) {
		return "https"
	}
	return "http"
}

// connectDialer opens a tunnel through the HTTP proxy p with CONNECT.
func connectDialer(p *url.URL, base dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		proxyAddr := p.Host
		if p.Port() == "" {
			proxyAddr = net.JoinHostPort(p.Hostname(), "80")
		}
		conn, err := base(ctx, network, proxyAddr)
		if err != nil {
			return nil, err
		}
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: http.Header{}}
		if p.User != nil {
			pass, _ := p.User.Password()
			req.SetBasicAuth(p.User.Username(), pass)
			req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
			req.Header.Del("Authorization")
		}
		if err := req.Write(conn); err != nil {
			conn.Close()
			return nil, err
		}
		br := bufio.NewReader(conn)
		rsp, err := http.ReadResponse(br, req)
		if err != nil {
			conn.Close()
			return nil, err
		}
		rsp.Body.Close()
		if rsp.StatusCode != http.StatusOK {
			conn.Close()
			return nil, fmt.Errorf("%w: %s", ErrProxyConnect, rsp.Status)
		}
		if br.Buffered() > 0 {
			conn.Close()
			return nil, errors.New("the proxy sent data before the tunnel was ready")
		}
		return conn, nil
	}
}
