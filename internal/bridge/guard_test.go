package bridge

// These tests reproduce finding C1 of the 2026-11-12 audit: a web page could
// drive the bridge with a cross-origin text/plain POST (no CORS preflight)
// and a foreign Host header (DNS rebinding), with no credential at all.

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

const testAddr = "127.0.0.1:18789"
const testToken = "secret-token"

func TestGuard(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		host       string
		auth       string
		ctype      string
		wantStatus int
	}{
		{"audit repro: page POST, foreign host, no token", "POST", "/", "evil.example:18789", "", "text/plain", fasthttp.StatusForbidden},
		{"foreign host with valid token", "POST", "/", "evil.example:18789", "Bearer " + testToken, "application/json", fasthttp.StatusForbidden},
		{"simple POST without token", "POST", "/", testAddr, "", "text/plain", fasthttp.StatusUnauthorized},
		{"wrong token", "POST", "/", testAddr, "Bearer nope", "application/json", fasthttp.StatusUnauthorized},
		{"token as raw value", "POST", "/", testAddr, testToken, "application/json", fasthttp.StatusUnauthorized},
		{"text/plain with valid token", "POST", "/", testAddr, "Bearer " + testToken, "text/plain", fasthttp.StatusUnsupportedMediaType},
		{"form with valid token", "POST", "/", testAddr, "Bearer " + testToken, "application/x-www-form-urlencoded", fasthttp.StatusUnsupportedMediaType},
		{"preflight", "OPTIONS", "/", testAddr, "", "", fasthttp.StatusUnauthorized},
		{"health from foreign host", "GET", "/health", "evil.example:18789", "", "", fasthttp.StatusForbidden},
		{"valid client", "POST", "/", testAddr, "Bearer " + testToken, "application/json", fasthttp.StatusTeapot},
		{"valid client, charset", "POST", "/", testAddr, "Bearer " + testToken, "application/json; charset=utf-8", fasthttp.StatusTeapot},
		{"valid client, localhost", "POST", "/", "localhost:18789", "Bearer " + testToken, "application/json", fasthttp.StatusTeapot},
		{"health needs no token", "GET", "/health", testAddr, "", "", fasthttp.StatusTeapot},
	}
	next := func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusTeapot) }
	h := Guard(next, testAddr, testToken)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ctx fasthttp.RequestCtx
			ctx.Request.Header.SetMethod(tc.method)
			ctx.Request.SetRequestURI(tc.path)
			ctx.Request.Header.SetHost(tc.host)
			if tc.auth != "" {
				ctx.Request.Header.Set("Authorization", tc.auth)
			}
			if tc.ctype != "" {
				ctx.Request.Header.SetContentType(tc.ctype)
			}
			h(&ctx)
			if got := ctx.Response.StatusCode(); got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", got, tc.wantStatus, ctx.Response.Body())
			}
		})
	}
}

// useTempConfigDir points os.UserConfigDir at a temporary directory.
func useTempConfigDir(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux and other Unix
	t.Setenv("HOME", dir)            // macOS: $HOME/Library/Application Support
	return dir
}

func TestListenAndServe_RequiresTokenFromFile(t *testing.T) {
	useTempConfigDir(t)
	addr := freeAddr(t)
	go ListenAndServe(addr) //nolint:errcheck

	path, err := TokenFile()
	if err != nil {
		t.Fatal(err)
	}
	token := waitForToken(t, path)
	if len(token) != 2*tokenBytes {
		t.Fatalf("token has %d characters, want %d", len(token), 2*tokenBytes)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("token file mode = %o, want 600", fi.Mode().Perm())
		}
	}

	post := func(auth string) int {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		rsp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(rsp)
		req.SetRequestURI("http://" + addr + "/")
		req.Header.SetMethod("POST")
		req.Header.SetContentType("application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		req.SetBodyString(`{}`) // no url: an authorized call reaches the handler and gets 400
		if err := fasthttp.Do(req, rsp); err != nil {
			t.Fatalf("POST: %v", err)
		}
		return rsp.StatusCode()
	}
	if got := post(""); got != fasthttp.StatusUnauthorized {
		t.Fatalf("POST without token: status %d, want 401", got)
	}
	if got := post("Bearer " + token); got != fasthttp.StatusBadRequest {
		t.Fatalf("POST with token: status %d, want 400 from the handler", got)
	}
}

// A second bridge that cannot bind the port must not replace the token of
// the bridge that runs, or every client of the running bridge breaks.
func TestListenAndServe_PortInUseKeepsToken(t *testing.T) {
	useTempConfigDir(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	path, _ := TokenFile()
	if err := writeToken(path, "running-bridge-token"); err != nil {
		t.Fatal(err)
	}
	if err := ListenAndServe(ln.Addr().String()); err == nil {
		t.Fatal("ListenAndServe on a used port succeeded")
	}
	got, _ := os.ReadFile(path)
	if strings.TrimSpace(string(got)) != "running-bridge-token" {
		t.Fatalf("token file changed to %q", got)
	}
}

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func waitForToken(t *testing.T, path string) string {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("token file %s did not appear", filepath.Base(path))
	return ""
}
