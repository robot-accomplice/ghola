package transport

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ErrRulesNeedDirect refuses --resolve, --connect-to and --ip through an
// HTTP proxy on the tls-client stack: tls-client takes a proxy URL or a
// dial function, not both, so the rules could not apply.
var ErrRulesNeedDirect = errors.New("--resolve, --connect-to, --ip, --interface, --local-port and --unix-socket with --impersonate work directly or through SOCKS, not through an HTTP proxy")

// anyHost is the host of a --resolve rule for every name, as in curl.
const anyHost = "*"

// netRules change where a TCP connection goes (plan phase 2, item 2). The
// host name stays as it is, so TLS and the Host header use the name of
// the URL, as curl does.
type netRules struct {
	resolve   map[string][]string // "host:port" or "*:port" -> addresses
	connectTo []connectRule       // in the order of the command line
	network   string              // "tcp4", "tcp6", or "" for both
}

// connectRule is one --connect-to HOST1:PORT1:HOST2:PORT2. An empty
// HOST1 or PORT1 matches any; an empty HOST2 or PORT2 keeps the URL's.
type connectRule struct {
	host, port, toHost, toPort string
}

// newNetRules reads the rules of opts. It returns nil when there are none.
// A bad rule is a bad option value.
func newNetRules(n config.NetOptions) (*netRules, error) {
	if len(n.Resolve) == 0 && len(n.ConnectTo) == 0 && n.IPVersion == "" {
		return nil, nil
	}
	r := &netRules{resolve: map[string][]string{}}
	switch n.IPVersion {
	case "", "any":
	case "4", "6":
		r.network = "tcp" + n.IPVersion
	default:
		return nil, config.BadOptionf("--ip %q: use 4, 6 or any", n.IPVersion)
	}
	for _, rule := range n.Resolve {
		if err := r.addResolve(rule); err != nil {
			return nil, config.BadOptionf("--resolve %q: %w", rule, err)
		}
	}
	for _, rule := range n.ConnectTo {
		c, err := parseConnectTo(rule)
		if err != nil {
			return nil, config.BadOptionf("--connect-to %q: %w", rule, err)
		}
		r.connectTo = append(r.connectTo, c)
	}
	return r, nil
}

// addResolve reads one --resolve rule in curl's forms: [+]HOST:PORT:ADDR
// [,ADDR]..., with "*" as HOST for any name, or -HOST:PORT, which removes
// an earlier rule. ghola keeps no DNS cache, so "+" (an entry that times
// out in curl) is the same as no prefix.
func (r *netRules) addResolve(rule string) error {
	if rest, ok := strings.CutPrefix(rule, "-"); ok {
		host, port, ok := cutHostPort(rest)
		if !ok || port == "" || strings.Contains(port, ":") {
			return errors.New("use -host:port")
		}
		delete(r.resolve, net.JoinHostPort(strings.ToLower(host), port))
		return nil
	}
	host, port, addrs, err := splitRule(strings.TrimPrefix(rule, "+"))
	if err != nil {
		return err
	}
	var ips []string
	for _, a := range strings.Split(addrs, ",") {
		a = strings.Trim(strings.TrimSpace(a), "[]")
		if net.ParseIP(a) == nil {
			return errors.New(a + " is not an IP address")
		}
		ips = append(ips, a)
	}
	r.resolve[net.JoinHostPort(host, port)] = ips
	return nil
}

// parseConnectTo reads HOST1:PORT1:HOST2:PORT2. A host may be an IPv6
// address in brackets; any part may be empty.
func parseConnectTo(rule string) (connectRule, error) {
	var parts [4]string
	rest := rule
	for i := range 3 {
		var ok bool
		if parts[i], rest, ok = cutField(rest); !ok {
			return connectRule{}, errors.New("use host:port:host2:port2")
		}
	}
	if strings.Contains(rest, ":") && !strings.HasPrefix(rest, "[") {
		return connectRule{}, errors.New("use host:port:host2:port2")
	}
	parts[3] = rest
	return connectRule{strings.ToLower(parts[0]), parts[1], parts[2], parts[3]}, nil
}

// cutField cuts the part of s before its first ":". An IPv6 address in
// brackets is one part.
func cutField(s string) (field, rest string, ok bool) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]:")
		if end < 0 {
			return "", "", false
		}
		return s[1:end], s[end+2:], true
	}
	return strings.Cut(s, ":")
}

// splitRule reads "host:port:rest", with an IPv6 host in brackets.
func splitRule(rule string) (host, port, rest string, err error) {
	host, after, ok := cutHostPort(rule)
	if !ok {
		return "", "", "", errors.New("use host:port:value")
	}
	port, rest, ok = strings.Cut(after, ":")
	if !ok || port == "" || rest == "" {
		return "", "", "", errors.New("use host:port:value")
	}
	return strings.ToLower(host), port, rest, nil
}

// cutHostPort splits "host:rest" or "[v6]:rest".
func cutHostPort(s string) (host, rest string, ok bool) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]:")
		if end < 0 {
			return "", "", false
		}
		return s[1:end], s[end+2:], true
	}
	host, rest, ok = strings.Cut(s, ":")
	return host, rest, ok && host != ""
}

// connect returns where --connect-to sends addr: the first rule that
// matches, or addr. r may be nil.
func (r *netRules) connect(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if r == nil || err != nil {
		return addr
	}
	for _, c := range r.connectTo {
		if (c.host == "" || strings.EqualFold(c.host, host)) && (c.port == "" || c.port == port) {
			if c.toHost != "" {
				host = c.toHost
			}
			if c.toPort != "" {
				port = c.toPort
			}
			return net.JoinHostPort(host, port)
		}
	}
	return addr
}

// addresses returns the --resolve addresses of addr, or nil. A rule for
// the name wins over a "*" rule, which serves names only. r may be nil.
func (r *netRules) addresses(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if r == nil || err != nil {
		return nil
	}
	if ips, ok := r.resolve[net.JoinHostPort(strings.ToLower(host), port)]; ok {
		return ips
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	return r.resolve[net.JoinHostPort(anyHost, port)]
}

// dial wraps base: --connect-to first, then --resolve, then --ip. Each
// --resolve address is tried in turn, as a name with several addresses.
func (r *netRules) dial(base dialFunc) dialFunc {
	if r == nil {
		return base
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		addr = r.connect(addr)
		if r.network != "" {
			network = r.network
		}
		ips := r.addresses(addr)
		if len(ips) == 0 {
			return base(ctx, network, addr)
		}
		_, port, _ := net.SplitHostPort(addr)
		var errs []error
		for _, ip := range ips {
			c, err := base(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				return c, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}
