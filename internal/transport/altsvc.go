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

// altSvcDefaultAge is the life of an Alt-Svc entry without ma= (RFC 7838,
// section 3.1).
const altSvcDefaultAge = 24 * time.Hour

// altSvcCache is the --alt-svc cache, in curl's file format (spec decision
// K). Each line is one alternative of one origin:
//
//	h2 example.com 443 h3 example.com 443 "20261122 10:00:00" 0 0
//
// The fields are the origin's ALPN, host and port, the alternative's ALPN,
// host and port, the expiry time, persist, and a priority that ghola
// writes as 0.
type altSvcCache struct {
	mu      sync.Mutex
	path    string
	entries []altSvcEntry
}

type altSvcEntry struct {
	srcALPN, srcHost string
	srcPort          int
	dstALPN, dstHost string
	dstPort          int
	expires          time.Time
	persist          bool
}

// loadAltSvc reads the cache at path. A file that does not exist yet is
// an empty cache.
func loadAltSvc(path string) (*altSvcCache, error) {
	c := &altSvcCache{path: path}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("--alt-svc: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e, ok := parseAltSvcLine(line)
		if !ok {
			return nil, fmt.Errorf("--alt-svc %s: bad line %q", path, line)
		}
		c.entries = append(c.entries, e)
	}
	return c, sc.Err()
}

func parseAltSvcLine(line string) (altSvcEntry, bool) {
	f := strings.Fields(line)
	if len(f) != 10 {
		return altSvcEntry{}, false
	}
	srcPort, err1 := strconv.Atoi(f[2])
	dstPort, err2 := strconv.Atoi(f[5])
	expires, err3 := time.Parse(hstsTimeFormat, strings.Trim(f[6]+" "+f[7], `"`))
	if err1 != nil || err2 != nil || err3 != nil {
		return altSvcEntry{}, false
	}
	return altSvcEntry{srcALPN: f[0], srcHost: strings.ToLower(f[1]), srcPort: srcPort, dstALPN: f[3],
		dstHost: strings.ToLower(f[4]), dstPort: dstPort, expires: expires, persist: f[8] == "1"}, true
}

// h3 reports an HTTP/3 alternative of the https origin u, on the same host
// and port, that has not expired. ghola uses only these: the HTTP/3 stack
// connects to the origin's own host and port.
func (c *altSvcCache) h3(u *fasthttp.URI) bool {
	host, port, ok := httpsOrigin(u)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, e := range c.entries {
		if e.srcHost == host && e.srcPort == port && e.dstALPN == "h3" && e.dstHost == host && e.dstPort == port && now.Before(e.expires) {
			return true
		}
	}
	return false
}

// record reads the Alt-Svc header of an https answer and saves the cache.
// The header replaces the origin's entries; "clear" removes them (RFC
// 7838, section 3). alpn is the protocol of the answer: h1 or h2.
func (c *altSvcCache) record(u *fasthttp.URI, alpn, header string) error {
	host, port, ok := httpsOrigin(u)
	if header = strings.TrimSpace(header); !ok || header == "" {
		return nil
	}
	var fresh []altSvcEntry
	if !strings.EqualFold(header, "clear") {
		for _, alt := range strings.Split(header, ",") {
			if e, ok := parseAlternative(alt, host); ok {
				e.srcALPN, e.srcHost, e.srcPort = alpn, host, port
				fresh = append(fresh, e)
			}
		}
		if len(fresh) == 0 {
			return nil // a header that ghola cannot read changes nothing
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.entries[:0]
	for _, e := range c.entries {
		if e.srcHost != host || e.srcPort != port {
			kept = append(kept, e)
		}
	}
	c.entries = append(kept, fresh...)
	return c.save()
}

// parseAlternative reads one alternative: h3=":443"; ma=86400; persist=1.
// An empty host in the authority is the origin's host.
func parseAlternative(alt, originHost string) (altSvcEntry, bool) {
	params := strings.Split(alt, ";")
	proto, authority, ok := strings.Cut(strings.TrimSpace(params[0]), "=")
	if !ok {
		return altSvcEntry{}, false
	}
	host, portText, err := net.SplitHostPort(strings.Trim(authority, `"`))
	port, perr := strconv.Atoi(portText)
	if err != nil || perr != nil {
		return altSvcEntry{}, false
	}
	if host == "" {
		host = originHost
	}
	e := altSvcEntry{dstALPN: proto, dstHost: strings.ToLower(host), dstPort: port, expires: time.Now().Add(altSvcDefaultAge).UTC()}
	for _, p := range params[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch strings.ToLower(k) {
		case "ma":
			if n, err := strconv.Atoi(v); err == nil {
				e.expires = time.Now().Add(time.Duration(n) * time.Second).UTC()
			}
		case "persist":
			e.persist = v == "1"
		}
	}
	return e, true
}

// save writes the cache in curl's format. The caller holds c.mu.
func (c *altSvcCache) save() error {
	var b strings.Builder
	b.WriteString("# Alt-Svc cache, in curl's format: https://curl.se/docs/alt-svc.html\n")
	for _, e := range c.entries {
		persist := 0
		if e.persist {
			persist = 1
		}
		fmt.Fprintf(&b, "%s %s %d %s %s %d \"%s\" %d 0\n", e.srcALPN, e.srcHost, e.srcPort,
			e.dstALPN, e.dstHost, e.dstPort, e.expires.UTC().Format(hstsTimeFormat), persist)
	}
	if err := os.WriteFile(c.path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("--alt-svc: %w", config.WriteError(err))
	}
	return nil
}

// httpsOrigin is the host and port of an https URI. Alt-Svc for http is
// not kept: HTTP/3 needs TLS.
func httpsOrigin(u *fasthttp.URI) (string, int, bool) {
	if !strings.EqualFold(string(u.Scheme()), "https") {
		return "", 0, false
	}
	hostport := string(u.Host())
	host, port := hostOnly(hostport), 443
	if _, p, err := net.SplitHostPort(hostport); err == nil {
		if port, err = strconv.Atoi(p); err != nil {
			return "", 0, false
		}
	}
	return strings.ToLower(host), port, true
}
