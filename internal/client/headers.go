package client

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// setHeaders sets every request header that comes from the options: the
// profile headers, -H, the User-Agent, Accept-Encoding
// and -u. FetchURL (stdout) and the download path both call it, so an option
// cannot reach one path and miss the other (audit finding H8).
func setHeaders(req *fasthttp.Request, opts *config.Options, active profile.BrowserProfile, overrides map[string]string, target, method string) {
	for _, generated := range profile.BuildHeaders(active, profile.HeaderOptions{
		Method:         method,
		TargetURL:      target,
		RefererMode:    opts.Stealth.Referer,
		ExplicitUA:     resolveUserAgent(opts, active),
		ExplicitLang:   opts.Stealth.AcceptLanguage,
		StealthHeaders: opts.Stealth.StealthHeaders,
	}) {
		if _, overridden := overrides[strings.ToLower(generated.Key)]; overridden {
			continue
		}
		req.Header.Set(generated.Key, generated.Value)
	}

	trusted := sendsCredentials(opts, target)
	if !trusted {
		noteWithheld(opts, target)
	}
	for _, h := range opts.Headers {
		key, val, ok := config.ParseHeader(h)
		if !ok || (!trusted && isCredentialHeader(key)) {
			continue
		}
		// -H "Name:" removes the header, as in curl (audit finding M7).
		// overrides keeps the profile and the defaults from setting it.
		if val == "" {
			req.Header.Del(key)
			continue
		}
		req.Header.Set(key, val)
	}

	if _, overridden := overrides["user-agent"]; !overridden {
		req.Header.Set("User-Agent", resolveUserAgent(opts, active))
	}

	if opts.Stealth.Compressed {
		req.Header.Set("Accept-Encoding", gholatransport.AcceptEncoding)
	}

	switch {
	case opts.User == "" || !trusted || waitsForChallenge(opts):
	case opts.SigV4 != nil:
		// Apply checked the first host. A host after a redirect gets the
		// key only with --follow-trusted; when its name holds no region,
		// the request goes unsigned, and ghola says so.
		if err := signSigV4(*opts.SigV4, req, opts.User, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v; the request is not signed\n", err)
		}
	default:
		auth := base64.StdEncoding.EncodeToString([]byte(opts.User))
		req.Header.Set("Authorization", "Basic "+auth)
	}
}

// sendsCredentials reports whether -u and the Authorization and Cookie
// headers from -H go to target. They are for the server the user named. A
// redirect to another scheme, host or port does not receive them unless
// --location-trusted is set, as in curl. Otherwise any server that redirects
// can collect them (audit finding H1).
func sendsCredentials(opts *config.Options, target string) bool {
	return opts.SendsCredentials(target)
}

// isCredentialHeader reports whether a -H header carries a credential.
func isCredentialHeader(key string) bool {
	return strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie")
}

// noteWithheld writes a note when this request carries no credentials
// because target is not the server they belong to, so a 401 from target
// can be explained. -q hides it.
func noteWithheld(opts *config.Options, target string) {
	if opts.Output.Silent || !hasCredentials(opts) {
		return
	}
	host := target
	if u, err := url.Parse(target); err == nil {
		host = u.Host
	}
	fmt.Fprintf(os.Stderr, "Note: the credentials are not sent to %s: they belong to another server (--follow-trusted sends them)\n", host)
}

// hasCredentials reports whether the run has -u, netrc or a credential
// header from -H.
func hasCredentials(opts *config.Options) bool {
	if opts.User != "" {
		return true
	}
	for _, h := range opts.Headers {
		if key, _, ok := config.ParseHeader(h); ok && isCredentialHeader(key) {
			return true
		}
	}
	return false
}
