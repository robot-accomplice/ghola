// Package bridge implements a local JSON-over-HTTP proxy that exposes ghola's
// stealth HTTP engine to external tools (e.g. the Rust "scope" companion).
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// Request is the JSON payload accepted by the bridge server.
type Request struct {
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers"`
	Body           string            `json:"body"`
	Drift          bool              `json:"drift"`
	Ghost          bool              `json:"ghost"`
	Retries        int               `json:"retries"`
	BufferSize     int               `json:"buffer_size"`
	Impersonate    string            `json:"impersonate"`
	StealthHeaders bool              `json:"stealth_headers"`
	CookieJar      string            `json:"cookie_jar"`
	Cookies        []string          `json:"cookies"`
	Proxy          string            `json:"proxy"`
	ProxyAuth      string            `json:"proxy_auth"`
	ProxyFile      string            `json:"proxy_file"`
	ProxyStrategy  string            `json:"proxy_strategy"`
	Referer        string            `json:"referer"`
	AcceptLanguage string            `json:"accept_language"`
	HTTP3          bool              `json:"http3"`
	Timeout        int               `json:"timeout"`
	Location       bool              `json:"location"`
	MaxRedirs      int               `json:"max_redirs"`
	RetryHTTP      bool              `json:"retry_http"`
	User           string            `json:"user"`
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
	return func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) == "/health" {
			ctx.SetContentType("application/json")
			ctx.SetBodyString(`{"status":"ok"}`)
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
				Ghost:          br.Ghost,
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

		req, rsp, err := client.FetchURL(context.Background(), opts, do)
		if err != nil {
			ctx.SetStatusCode(fasthttp.StatusBadGateway)
			writeError(ctx, err.Error())
			return
		}
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)

		rspHeaders := make(map[string]string)
		rsp.Header.VisitAll(func(key, value []byte) {
			rspHeaders[string(key)] = string(value)
		})

		out, err := json.Marshal(Response{
			StatusCode: rsp.StatusCode(),
			Headers:    rspHeaders,
			Body:       string(rsp.Body()),
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

func writeError(ctx *fasthttp.RequestCtx, msg string) {
	ctx.SetContentType("application/json")
	out, err := json.Marshal(Response{Error: msg})
	if err != nil {
		ctx.SetBodyString(`{"error":"marshal failed"}`)
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
// real network transport.
func ListenAndServe(addr string) error {
	fmt.Fprintf(os.Stderr, "ghola bridge listening on %s\n", addr)
	return fasthttp.ListenAndServe(addr, Handler(nil))
}
