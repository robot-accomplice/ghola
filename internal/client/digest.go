package client

import (
	"context"
	"crypto/md5" //nolint:gosec // RFC 7616 Digest names MD5; ghola must speak it to servers that use it
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// errAuthChallenge stops a download at a 401 that ghola answers with
// --auth digest or any, before the file opens.
var errAuthChallenge = errors.New("authentication challenge")

// answersChallenge reports whether a 401 gets one more request with the
// credentials: --auth digest or any, with -u.
func answersChallenge(opts *config.Options, status int) bool {
	return status == fasthttp.StatusUnauthorized && opts.User != "" && waitsForChallenge(opts)
}

// waitsForChallenge: --auth digest and any send no credentials before
// the server asks, as curl --digest and --anyauth do.
func waitsForChallenge(opts *config.Options) bool {
	return opts.Auth == "digest" || opts.Auth == "any"
}

// authAnswer returns the Authorization value for the challenges of a 401:
// Digest when the server offers it, else Basic with --auth any. ok is
// false when no challenge fits.
func authAnswer(opts *config.Options, challenges []string, method, uri string) (string, bool) {
	for _, c := range challenges {
		if scheme, params, _ := strings.Cut(strings.TrimSpace(c), " "); strings.EqualFold(scheme, "Digest") {
			cnonce := make([]byte, 12)
			if _, err := rand.Read(cnonce); err != nil {
				return "", false
			}
			v, err := digestAnswer(opts.User, parseAuthParams(params), method, uri, hex.EncodeToString(cnonce))
			return v, err == nil
		}
	}
	if opts.Auth == "any" {
		for _, c := range challenges {
			if scheme, _, _ := strings.Cut(strings.TrimSpace(c), " "); strings.EqualFold(scheme, "Basic") {
				return "Basic " + base64.StdEncoding.EncodeToString([]byte(opts.User)), true
			}
		}
	}
	return "", false
}

// parseAuthParams reads name=value pairs; a quoted value may hold commas.
func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimLeft(strings.TrimSpace(s), ",") {
		name, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		name, rest = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(rest)
		var value string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			value, rest = rest[1:1+end], rest[2+end:]
		} else {
			value, rest, _ = strings.Cut(rest, ",")
		}
		out[name] = strings.TrimSpace(value)
		s = rest
	}
	return out
}

// digestAnswer computes the RFC 7616 answer, with qop auth when the
// server offers it. It takes MD5, SHA-256 and their -sess forms.
func digestAnswer(user string, ch map[string]string, method, uri, cnonce string) (string, error) {
	name, pass, _ := strings.Cut(user, ":")
	algo := ch["algorithm"]
	if algo == "" {
		algo = "MD5"
	}
	var newHash func() hash.Hash
	switch strings.TrimSuffix(strings.ToUpper(algo), "-SESS") {
	case "MD5":
		newHash = md5.New
	case "SHA-256":
		newHash = sha256.New
	default:
		return "", fmt.Errorf("--auth digest: algorithm %s is not supported", algo)
	}
	h := func(parts ...string) string {
		d := newHash()
		io.WriteString(d, strings.Join(parts, ":")) //nolint:errcheck // a hash does not fail
		return hex.EncodeToString(d.Sum(nil))
	}
	const nc = "00000001" // ghola answers each challenge once
	ha1 := h(name, ch["realm"], pass)
	if strings.HasSuffix(strings.ToUpper(algo), "-SESS") {
		ha1 = h(ha1, ch["nonce"], cnonce)
	}
	ha2 := h(method, uri)
	qop := ""
	for _, q := range strings.Split(ch["qop"], ",") {
		if strings.TrimSpace(q) == "auth" {
			qop = "auth"
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=%s`, name, ch["realm"], ch["nonce"], uri, algo)
	if qop != "" {
		fmt.Fprintf(&b, `, response="%s", qop=%s, nc=%s, cnonce="%s"`, h(ha1, ch["nonce"], nc, cnonce, qop, ha2), qop, nc, cnonce)
	} else {
		fmt.Fprintf(&b, `, response="%s"`, h(ha1, ch["nonce"], ha2))
	}
	if op, ok := ch["opaque"]; ok {
		fmt.Fprintf(&b, `, opaque="%s"`, op)
	}
	return b.String(), nil
}

// withAuthAnswer returns do with --auth digest and any on the stdout
// path: a 401 with a challenge gets one more request with the answer.
func withAuthAnswer(do Doer) Doer {
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response) error {
		if err := do(ctx, opts, req, rsp); err != nil || !answersChallenge(opts, rsp.StatusCode()) {
			return err
		}
		var challenges []string
		for _, v := range rsp.Header.PeekAll("WWW-Authenticate") {
			challenges = append(challenges, string(v))
		}
		if !retryWithAnswer(opts, req, challenges) {
			return nil
		}
		rsp.Reset()
		return do(ctx, opts, req, rsp)
	}
}

// withAuthStream is withAuthAnswer for the download path. The check stops
// the 401 with errAuthChallenge, so its body reaches no file.
func withAuthStream(stream Streamer) Streamer {
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		meta, err := stream(ctx, opts, req, sink)
		if errors.Is(err, errAuthChallenge) && retryWithAnswer(opts, req, meta.Header.Values("WWW-Authenticate")) {
			meta, err = stream(ctx, opts, req, sink)
		}
		if errors.Is(err, errAuthChallenge) {
			// The server refused the answer, or offered no challenge
			// that fits: the 401 is the answer, as without --auth.
			err = fmt.Errorf("%w: %d", config.ErrHTTPStatus, meta.StatusCode)
		}
		return meta, err
	}
}

// retryWithAnswer sets the answer on req. The credentials go only to the
// server the user named, as -u does (audit finding H1).
func retryWithAnswer(opts *config.Options, req *fasthttp.Request, challenges []string) bool {
	target := string(req.URI().FullURI())
	if !sendsCredentials(opts, target) {
		return false
	}
	v, ok := authAnswer(opts, challenges, string(req.Header.Method()), string(req.URI().RequestURI()))
	if ok {
		req.Header.Set("Authorization", v)
	}
	return ok
}
