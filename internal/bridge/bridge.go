// Package bridge implements a local JSON-over-HTTP proxy that exposes ghola's
// stealth HTTP engine to external tools (e.g. the Rust "scope" companion).
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Request is the JSON payload accepted by the bridge server.
type Request struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Drift   bool              `json:"drift"`
	// Ghost is accepted and ignored. v0 sent an X-Ghola-Identity header
	// for it, which marked each request as coming from ghola; scope and
	// scope-bca set it in their stealth mode (v1 spec, question A).
	Ghost          bool     `json:"ghost"`
	Retries        int      `json:"retries"`
	BufferSize     int      `json:"buffer_size"`
	Impersonate    string   `json:"impersonate"`
	StealthHeaders bool     `json:"stealth_headers"`
	CookieJar      string   `json:"cookie_jar"`
	Cookies        []string `json:"cookies"`
	Proxy          string   `json:"proxy"`
	ProxyAuth      string   `json:"proxy_auth"`
	ProxyFile      string   `json:"proxy_file"`
	ProxyStrategy  string   `json:"proxy_strategy"`
	Referer        string   `json:"referer"`
	AcceptLanguage string   `json:"accept_language"`
	HTTP3          bool     `json:"http3"`
	Timeout        int      `json:"timeout"`
	Location       bool     `json:"location"`
	MaxRedirs      int      `json:"max_redirs"`
	RetryHTTP      bool     `json:"retry_http"`
	User           string   `json:"user"`
}

// Response is the JSON payload returned by the bridge server.
type Response struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Error      string            `json:"error,omitempty"`
}

// Addr is the default listen address for the bridge sidecar.
const Addr = "127.0.0.1:18789"

// Handler processes a single bridge request. Exported for testing with
// custom Doer injection; production callers should use ListenAndServe.
func Handler(do client.Doer) fasthttp.RequestHandler {
	rpc := newRPC(nil) // the jobs live as long as the bridge
	root := rpc.root   // the folder where ghola serve runs: files that a client names stay below it
	return func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) == "/health" {
			ctx.SetContentType("application/json")
			ctx.SetBodyString(`{"status":"ok"}`)
			return
		}

		if string(ctx.Path()) == RPCPath && (ctx.IsPost() || websocket.FastHTTPIsWebSocketUpgrade(ctx)) {
			rpc.serve(ctx)
			return
		}
		if !ctx.IsPost() {
			ctx.SetStatusCode(fasthttp.StatusMethodNotAllowed)
			return
		}

		var br Request
		if err := json.Unmarshal(ctx.PostBody(), &br); err != nil {
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			writeError(ctx, "invalid json: "+err.Error())
			return
		}

		if br.URL == "" {
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			writeError(ctx, "url is required")
			return
		}
		if br.CookieJar != "" && !insideFolder(root, br.CookieJar) {
			log.Printf("ghola bridge: refused cookie_jar %q: outside the folder where ghola serve runs", br.CookieJar)
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			writeError(ctx, "cookie_jar must be a file below the folder where ghola serve runs")
			return
		}
		if !bridgeScheme(br.URL) {
			// A bridge client sends URLs from outside data. It must not
			// make ghola read a local file (file://) or speak another
			// protocol for it.
			ctx.SetStatusCode(fasthttp.StatusBadRequest)
			writeError(ctx, "url must be http:// or https://")
			return
		}

		method := br.Method
		if method == "" {
			method = config.DefaultMethod
		}

		bufSize := br.BufferSize
		if bufSize == 0 {
			bufSize = config.DefaultBufferSize
		}

		opts := &config.Options{
			URL:     br.URL,
			Method:  method,
			Data:    br.Body,
			User:    br.User,
			Headers: nil,
			Stealth: config.StealthOptions{
				Agent:          config.DefaultAgent,
				Impersonate:    br.Impersonate,
				StealthHeaders: br.StealthHeaders,
				CookieJar:      br.CookieJar,
				Cookies:        br.Cookies,
				Referer:        br.Referer,
				AcceptLanguage: br.AcceptLanguage,
				HTTP3:          br.HTTP3,
			},
			Resilience: config.ResilienceOptions{
				Retries:    br.Retries,
				Backoff:    config.DefaultBackoffMs,
				BufferSize: bufSize,
				Timeout:    br.Timeout,
				Location:   br.Location,
				MaxRedirs:  maxRedirs(br.MaxRedirs),
				RetryHTTP:  br.RetryHTTP,
				// The bridge fetches http(s) only, also after a redirect.
				HTTPRedirectsOnly: true,
			},
			Output: config.OutputOptions{
				Silent: true,
			},
			ProxyCfg: config.ProxyOptions{
				Proxy:    br.Proxy,
				Auth:     br.ProxyAuth,
				File:     br.ProxyFile,
				Strategy: br.ProxyStrategy,
			},
		}
		if br.Drift {
			opts.Stealth.Drift = config.DefaultDriftMs
		}

		for k, v := range br.Headers {
			opts.Headers = append(opts.Headers, k+": "+v)
		}

		fetchCtx, cancel := client.WithTimeout(context.Background(), opts)
		defer cancel()
		req, rsp, err := client.FetchURL(fetchCtx, opts, do)
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusBadGateway)
			writeError(ctx, err.Error())
			return
		}
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)
		body, err := gholatransport.ReadBody(rsp)
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusBadGateway)
			writeError(ctx, err.Error())
			return
		}

		rspHeaders := make(map[string]string)
		for key, value := range rsp.Header.All() {
			k, v := string(key), string(value)
			// A repeated header keeps every value, joined as HTTP allows.
			// Set-Cookie values hold commas (Expires), so the last one stays.
			if prev, ok := rspHeaders[k]; ok && !strings.EqualFold(k, "Set-Cookie") {
				v = prev + ", " + v
			}
			rspHeaders[k] = v
		}

		out, err := json.Marshal(Response{
			StatusCode: rsp.StatusCode(),
			Headers:    rspHeaders,
			Body:       string(body),
		})
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetContentType("application/json")
			ctx.SetBodyString(`{"error":"response marshal failed"}`)
			return
		}

		ctx.SetContentType("application/json")
		ctx.SetBody(out)
	}
}

// writeError writes a Response that carries msg. Headers is an empty map, not
// null: clients that decode headers into a non-optional map (scope, scope-bca)
// fail on null and then show a parse error instead of msg.
func writeError(ctx *fasthttp.RequestCtx, msg string) {
	ctx.SetContentType("application/json")
	out, err := json.Marshal(Response{Headers: map[string]string{}, Error: msg})
	if err != nil {
		ctx.SetBodyString(`{"status_code":0,"headers":{},"body":"","error":"marshal failed"}`) // the shape that clients parse
		return
	}
	ctx.SetBody(out)
}

func maxRedirs(n int) int {
	if n > 0 {
		return n
	}
	return config.DefaultMaxRedirs
}

// ListenAndServe starts the bridge server on the given address using the
// real network transport. It writes a new token to TokenFile after it binds
// the port, so a second bridge that fails to bind never replaces the token
// of the bridge that runs. Clients must send "Authorization: Bearer <token>".
func ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return err
	}
	token, err := newToken()
	if err != nil {
		ln.Close()
		return fmt.Errorf("make bridge token: %w", err)
	}
	path, err := TokenFile()
	if err == nil {
		err = writeToken(path, token)
	}
	if err != nil {
		ln.Close()
		return fmt.Errorf("write bridge token: %w", err)
	}
	fmt.Fprintf(os.Stderr, "ghola bridge listening on %s (token in %s)\n", addr, path)
	return newServer(Guard(Handler(nil), addr, token), bridgeReadTimeout, bridgeIdleTimeout).Serve(ln)
}

// bridgeScheme reports whether the bridge may fetch raw: http and https
// only.
func bridgeScheme(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	}
	return false
}

// The limits of the bridge server. The read timeout covers only the
// client's request: the upstream fetch runs in the handler, and clients
// have their own request timeout. A WebSocket connection is hijacked, so
// these limits do not end it.
const (
	bridgeReadTimeout = 30 * time.Second
	bridgeIdleTimeout = 2 * time.Minute
	maxBridgeBody     = 4 << 20 // fasthttp's default, now set on purpose
)

// newServer returns the bridge server: handler with the read and idle
// timeouts and the body limit. An error before the handler (a body that is
// too large, a request that does not parse, a timeout) gets the reply
// shape that clients parse: status_code, headers, body and error.
func newServer(handler fasthttp.RequestHandler, read, idle time.Duration) *fasthttp.Server {
	return &fasthttp.Server{
		Handler:            handler,
		ReadTimeout:        read,
		IdleTimeout:        idle,
		MaxRequestBodySize: maxBridgeBody,
		ErrorHandler: func(ctx *fasthttp.RequestCtx, err error) {
			log.Printf("ghola bridge: refused a request from %s: %v", ctx.RemoteAddr(), err)
			status := fasthttp.StatusBadRequest
			if errors.Is(err, fasthttp.ErrBodyTooLarge) {
				status = fasthttp.StatusRequestEntityTooLarge
			}
			ctx.SetStatusCode(status)
			writeError(ctx, err.Error())
		},
	}
}
