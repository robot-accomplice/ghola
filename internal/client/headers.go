package client

import (
	"encoding/base64"
	"net/url"
	"strings"

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

	if opts.User != "" && trusted && !waitsForChallenge(opts) {
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
	return opts.Resilience.LocationTrusted || sameServer(opts.URL, target)
}

// isCredentialHeader reports whether a -H header carries a credential.
func isCredentialHeader(key string) bool {
	return strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie")
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
