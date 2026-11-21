package transport

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// hstsTimeFormat is the expiry format of curl's HSTS cache file.
const hstsTimeFormat = "20060102 15:04:05"

// hstsCache is the --hsts cache, in curl's file format: one host on each
// line with its expiry time; a leading dot covers the subdomains too.
type hstsCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]hstsEntry
}

type hstsEntry struct {
	expires time.Time // zero is "unlimited"
	subs    bool
}

// loadHSTS reads the cache at path. A file that does not exist yet is an
// empty cache: ghola writes it after the first HTTPS answer with
// Strict-Transport-Security.
func loadHSTS(path string) (*hstsCache, error) {
	c := &hstsCache{path: path, entries: map[string]hstsEntry{}}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("--hsts: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		host, when, _ := strings.Cut(line, " ")
		e := hstsEntry{subs: strings.HasPrefix(host, ".")}
		if when = strings.Trim(strings.TrimSpace(when), `"`); when != "unlimited" {
			if e.expires, err = time.Parse(hstsTimeFormat, when); err != nil {
				return nil, fmt.Errorf("--hsts %s: bad line %q", path, line)
			}
		}
		c.entries[strings.ToLower(strings.TrimPrefix(host, "."))] = e
	}
	return c, sc.Err()
}

// upgrade turns an http URI into https when the cache holds its host, as
// a browser does. Port 80 becomes the https default.
func (c *hstsCache) upgrade(u *fasthttp.URI) {
	if !strings.EqualFold(string(u.Scheme()), "http") || !c.known(hostOnly(string(u.Host()))) {
		return
	}
	u.SetScheme("https")
	if host, port, err := net.SplitHostPort(string(u.Host())); err == nil && port == "80" {
		u.SetHost(host)
	}
}

// known reports a host, or a parent with includeSubDomains, that has not
// expired.
func (c *hstsCache) known(host string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for name, first := strings.ToLower(host), true; name != ""; first = false {
		if e, ok := c.entries[name]; ok && (first || e.subs) && (e.expires.IsZero() || now.Before(e.expires)) {
			return true
		}
		_, name, _ = strings.Cut(name, ".")
	}
	return false
}

// record reads Strict-Transport-Security from an HTTPS answer and saves
// the cache. max-age=0 removes the host. An IP address is never cached,
// as in curl.
func (c *hstsCache) record(u *fasthttp.URI, header string) error {
	host := strings.ToLower(hostOnly(string(u.Host())))
	if header == "" || !strings.EqualFold(string(u.Scheme()), "https") || net.ParseIP(host) != nil {
		return nil
	}
	maxAge, subs := -1, false
	for _, d := range strings.Split(header, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(d), "=")
		switch strings.ToLower(k) {
		case "max-age":
			n, err := strconv.Atoi(strings.Trim(v, `"`))
			if err != nil {
				return nil // a bad header is ignored, as RFC 6797 says
			}
			maxAge = n
		case "includesubdomains":
			subs = true
		}
	}
	if maxAge < 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if maxAge == 0 {
		delete(c.entries, host)
	} else {
		c.entries[host] = hstsEntry{expires: time.Now().Add(time.Duration(maxAge) * time.Second).UTC(), subs: subs}
	}
	return c.save()
}

// save writes the cache in curl's format. The caller holds c.mu.
func (c *hstsCache) save() error {
	var b strings.Builder
	b.WriteString("# HSTS cache, in curl's format: https://curl.se/docs/hsts.html\n")
	for host, e := range c.entries {
		if e.subs {
			host = "." + host
		}
		when := "unlimited"
		if !e.expires.IsZero() {
			when = e.expires.UTC().Format(hstsTimeFormat)
		}
		fmt.Fprintf(&b, "%s \"%s\"\n", host, when)
	}
	if err := os.WriteFile(c.path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("--hsts: %w", config.WriteError(err))
	}
	return nil
}

// hostOnly strips the port.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
