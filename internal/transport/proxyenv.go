package transport

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// proxyFromEnv returns the proxy for target from the environment, or nil
// (audit finding M8). It reads http_proxy or HTTP_PROXY for http, and
// https_proxy or HTTPS_PROXY for https, then skips the hosts in no_proxy or
// NO_PROXY. It differs from Go's http.ProxyFromEnvironment in two ways:
//   - It proxies localhost and loopback addresses, as curl and wget do.
//   - It reads the environment on each call. Go reads it once per process.
//
// HTTP_PROXY is ignored when REQUEST_METHOD is set, as Go does: in a CGI
// program a client can set it through the Proxy request header (httpoxy).
func proxyFromEnv(target *url.URL) (*url.URL, error) {
	var raw string
	switch strings.ToLower(target.Scheme) {
	case "http":
		raw = os.Getenv("http_proxy")
		if raw == "" && os.Getenv("REQUEST_METHOD") == "" {
			raw = os.Getenv("HTTP_PROXY")
		}
	case "https":
		raw = firstEnv("https_proxy", "HTTPS_PROXY")
	}
	if raw == "" || noProxy(target.Hostname(), firstEnv("no_proxy", "NO_PROXY")) {
		return nil, nil
	}
	// A value without a scheme is an HTTP proxy, as in curl.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	p, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy in the environment %q: %w", raw, err)
	}
	return p, nil
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// noProxy reports whether host matches the no_proxy list: "*", a host name
// or a domain suffix (with or without a leading dot), an IP address, or a
// CIDR range.
func noProxy(host, list string) bool {
	host = strings.ToLower(host)
	ip := net.ParseIP(host)
	for _, entry := range strings.Split(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		entry = strings.TrimPrefix(entry, ".")
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}
