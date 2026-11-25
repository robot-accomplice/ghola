package config

import (
	"net/url"
	"strings"

	"github.com/robot-accomplice/ghola/internal/netrc"
)

// SendsCredentials reports whether -u, --auth-bearer, netrc and the
// Authorization and Cookie headers from -H go to target. They belong to
// the server of CredentialURL (the first URL of the run), or of URL when
// CredentialURL is not set. A redirect, another URL of the run, or an FTP
// or SSH server reached by a redirect does not get them unless
// --follow-trusted (audit finding H1; spec decision 2026-11-25). With no
// URL at all, the request is the only one and gets them.
func (o *Options) SendsCredentials(target string) bool {
	if o.Resilience.LocationTrusted {
		return true
	}
	base := o.CredentialURL
	if base == "" {
		base = o.URL
	}
	return base == "" || SameServer(base, target)
}

// SameServer reports whether a and b have the same scheme, host and port.
func SameServer(a, b string) bool {
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

// UseNetrc sets the credentials for URL from the netrc entries that
// --auth-netrc or --auth-netrc-file read, unless -u gave them. Each URL of
// a run looks up its own host, so one host's entry never goes to another.
// A user in the URL picks its entry.
func (o *Options) UseNetrc() {
	if len(o.Netrc) == 0 || o.UserFromFlag {
		return
	}
	o.User = ""
	u, err := url.Parse(o.URL)
	if err != nil {
		return
	}
	if e, ok := netrc.Find(o.Netrc, u.Hostname(), u.User.Username()); ok {
		o.User = e.Login + ":" + e.Password
		o.CredentialURL = o.URL
	}
}
