package bridge

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"os"
	"path/filepath"

	"github.com/valyala/fasthttp"
)

// The bridge sends real requests from the user's machine, so it must refuse
// callers other than a local client of the same user. A web page can reach
// 127.0.0.1 too: it can send a "simple" cross-origin POST without a CORS
// preflight, and DNS rebinding lets it read the answers. Three checks stop it:
//
//  1. Host must be the bridge address. This stops DNS rebinding.
//  2. Authorization must carry the token from TokenFile. Only processes that
//     can read the user's config directory know it.
//  3. Content-Type must be application/json. A browser sends that type
//     cross-origin only after a preflight, which the bridge never approves.

// tokenBytes is the size of the random bridge token before hex encoding.
const tokenBytes = 32

// TokenFile returns the path of the file that holds the bridge token. scope
// reads the same path (scope/src/http/ghola.rs).
func TokenFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "ghola", "bridge-token"), nil
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeToken writes token to path with owner-only permissions. It writes a
// temporary file first, so a reader never sees a partial token.
func writeToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Guard wraps next with the Host, token and Content-Type checks. addr is the
// listen address ("127.0.0.1:18789"). GET /health needs only the Host check.
func Guard(next fasthttp.RequestHandler, addr, token string) fasthttp.RequestHandler {
	_, port, _ := net.SplitHostPort(addr)
	hosts := map[string]bool{addr: true, "localhost:" + port: true}
	want := []byte("Bearer " + token)
	return func(ctx *fasthttp.RequestCtx) {
		switch {
		case !hosts[string(ctx.Host())]:
			reject(ctx, fasthttp.StatusForbidden, "host not allowed")
		case string(ctx.Path()) == "/health":
			next(ctx)
		case subtle.ConstantTimeCompare(ctx.Request.Header.Peek("Authorization"), want) != 1:
			reject(ctx, fasthttp.StatusUnauthorized, "missing or wrong bridge token")
		case !isJSON(ctx.Request.Header.ContentType()):
			reject(ctx, fasthttp.StatusUnsupportedMediaType, "content type must be application/json")
		default:
			next(ctx)
		}
	}
}

func isJSON(contentType []byte) bool {
	mt, _, err := mime.ParseMediaType(string(contentType))
	return err == nil && mt == "application/json"
}

// reject answers with a JSON error and logs one line to stderr, so a refused
// client can be traced after the fact. The token is never logged.
func reject(ctx *fasthttp.RequestCtx, status int, msg string) {
	fmt.Fprintf(os.Stderr, "ghola bridge: rejected %s %s from %s (host %q): %s\n",
		ctx.Method(), ctx.Path(), ctx.RemoteAddr(), ctx.Host(), msg)
	ctx.SetStatusCode(status)
	writeError(ctx, msg)
}
