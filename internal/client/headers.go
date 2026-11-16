package client

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	"github.com/valyala/fasthttp"
)

// setHeaders sets every request header that comes from the options: the
// profile headers, -H, the User-Agent, the ghost signature, Accept-Encoding
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

	for _, h := range opts.Headers {
		if key, val, ok := parseHeader(h); ok {
			req.Header.Set(key, val)
		}
	}

	if _, overridden := overrides["user-agent"]; !overridden {
		req.Header.Set("User-Agent", resolveUserAgent(opts, active))
	}

	if opts.Stealth.Ghost {
		applyGhostSign(req, target)
	}

	if opts.Stealth.Compressed {
		req.Header.Set("Accept-Encoding", "gzip")
	}

	// -u is for the server the user named. A redirect to another server must
	// not receive the password (audit finding H1; curl does the same).
	if opts.User != "" && sameServer(opts.URL, target) {
		auth := base64.StdEncoding.EncodeToString([]byte(opts.User))
		req.Header.Set("Authorization", "Basic "+auth)
	}
}

// sameServer reports whether a and b have the same scheme, host and port.
func sameServer(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		effectivePort(ua) == effectivePort(ub)
}

// effectivePort returns the URL's port, or the default port of its scheme.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
