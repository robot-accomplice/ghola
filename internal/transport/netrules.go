package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ErrRulesNeedDirect refuses --resolve, --connect-to and --ip through an
// HTTP proxy on the tls-client stack: tls-client takes a proxy URL or a
// dial function, not both, so the rules could not apply.
var ErrRulesNeedDirect = errors.New("--resolve, --connect-to and --ip with --impersonate work directly or through SOCKS, not through an HTTP proxy")

// netRules change where a TCP connection goes (plan phase 2, item 2). The
// host name stays as it is, so TLS and the Host header use the name of
// the URL, as curl does.
type netRules struct {
	resolve   map[string]string // "host:port" -> address
	connectTo map[string]string // "host:port" -> "host2:port2"
	network   string            // "tcp4", "tcp6", or "" for both
}

// newNetRules reads the rules of opts. It returns nil when there are none.
func newNetRules(n config.NetOptions) (*netRules, error) {
	if len(n.Resolve) == 0 && len(n.ConnectTo) == 0 && n.IPVersion == "" {
		return nil, nil
	}
	r := &netRules{resolve: map[string]string{}, connectTo: map[string]string{}}
	switch n.IPVersion {
	case "", "any":
	case "4", "6":
		r.network = "tcp" + n.IPVersion
	default:
		return nil, fmt.Errorf("--ip %q: use 4, 6 or any", n.IPVersion)
	}
	for _, rule := range n.Resolve {
		host, port, addr, err := splitRule(rule)
		if err != nil {
			return nil, fmt.Errorf("--resolve %q: %w", rule, err)
		}
		first, _, _ := strings.Cut(addr, ",")
		first = strings.Trim(first, "[]")
		if net.ParseIP(first) == nil {
			return nil, fmt.Errorf("--resolve %q: %q is not an IP address", rule, first)
		}
		r.resolve[net.JoinHostPort(host, port)] = first
	}
	for _, rule := range n.ConnectTo {
		host, port, to, err := splitRule(rule)
		if err != nil {
			return nil, fmt.Errorf("--connect-to %q: %w", rule, err)
		}
		toHost, toPort, ok := cutHostPort(to)
		if !ok {
			return nil, fmt.Errorf("--connect-to %q: the target must be host:port", rule)
		}
		r.connectTo[net.JoinHostPort(host, port)] = net.JoinHostPort(toHost, toPort)
	}
	return r, nil
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

// dial wraps base: --connect-to first, then --resolve, then --ip.
func (r *netRules) dial(base dialFunc) dialFunc {
	if r == nil {
		return base
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		key := lowerHost(addr)
		if to, ok := r.connectTo[key]; ok {
			addr, key = to, lowerHost(to)
		}
		if ip, ok := r.resolve[key]; ok {
			_, port, _ := net.SplitHostPort(addr)
			addr = net.JoinHostPort(ip, port)
		}
		if r.network != "" {
			network = r.network
		}
		return base(ctx, network, addr)
	}
}

func lowerHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return net.JoinHostPort(strings.ToLower(host), port)
}
