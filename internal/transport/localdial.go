package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/robot-accomplice/ghola/internal/config"
)

// wsaeAddrInUse is Windows' "address in use" error, which is not
// syscall.EADDRINUSE there.
const wsaeAddrInUse = syscall.Errno(10048)

// hasLocalOptions reports --interface, --local-port, --unix-socket or
// --doh.
func hasLocalOptions(n config.NetOptions) bool {
	return n.Interface != "" || n.LocalPort != "" || n.UnixSocket != "" || n.DoH != ""
}

// dialPlan is how ghola reaches a target. direct dials it with
// --connect-to, --resolve and --ip, then --doh, then the local options.
// A SOCKS proxy that leaves the name to ghola (socks4, socks5) resolves it
// with the same rules and lookup, and one that resolves it itself
// (socks4a, socks5h) still gets the --connect-to target. Every protocol
// takes its dial from here, so a rule cannot reach one protocol and miss
// another.
type dialPlan struct {
	direct dialFunc
	rules  *netRules // nil without rules
	// lookup resolves a name: --doh, else the system resolver.
	lookup func(ctx context.Context, host string) ([]net.IP, error)
}

// newDialPlan builds the dial plan of opts. The --unix-socket serves HTTP
// only, as in curl, so FTP and SSH pass unixOK false.
func newDialPlan(opts *config.Options, unixOK bool) (*dialPlan, error) {
	rules, err := newNetRules(opts.Net)
	if err != nil {
		return nil, err
	}
	local, doh, err := localDial(opts, unixOK)
	if err != nil {
		return nil, err
	}
	p := &dialPlan{direct: rules.dial(local), rules: rules, lookup: systemLookup(rules)}
	if doh != nil {
		p.lookup = doh.lookup
	}
	return p, nil
}

// systemLookup resolves with the system resolver, for the --ip family.
func systemLookup(rules *netRules) func(ctx context.Context, host string) ([]net.IP, error) {
	network := "ip"
	if rules != nil && rules.network != "" {
		network = "ip" + strings.TrimPrefix(rules.network, "tcp")
	}
	return func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, network, host)
	}
}

// localDial returns the base dial function: from the --interface address
// and a --local-port in its range, or to the --unix-socket, with --doh
// resolving the name. doh is nil without --doh.
func localDial(opts *config.Options, unixOK bool) (dialFunc, *dohResolver, error) {
	n := opts.Net
	d, err := sourceDial(n, unixOK)
	if err != nil || n.DoH == "" || (n.UnixSocket != "" && unixOK) {
		return d, nil, err
	}
	network := ""
	if n.IPVersion == "4" || n.IPVersion == "6" {
		network = "tcp" + n.IPVersion
	}
	doh, err := newDoH(n.DoH, network, opts.Stealth)
	if err != nil {
		return nil, nil, err
	}
	return doh.dial(d), doh, nil // --doh resolves the name
}

// sourceDial is localDial without --doh.
func sourceDial(n config.NetOptions, unixOK bool) (dialFunc, error) {
	if n.UnixSocket != "" && unixOK {
		path := n.UnixSocket
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}, nil
	}
	if n.Interface == "" && n.LocalPort == "" {
		return (&net.Dialer{}).DialContext, nil
	}
	ips, err := interfaceIPs(n.Interface)
	if err != nil {
		return nil, err
	}
	first, last, err := portRange(n.LocalPort)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		sources := sourcesFor(ips, network, addr)
		if len(sources) == 0 {
			return nil, fmt.Errorf("--interface %s has no address for %s", n.Interface, addr)
		}
		var errs []error
		for _, ip := range sources {
			c, err := dialFrom(ctx, ip, first, last, network, addr)
			if err == nil {
				return c, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}, nil
}

// dialFrom dials addr from ip, on the first free port of first..last (0
// is any port).
func dialFrom(ctx context.Context, ip net.IP, first, last int, network, addr string) (net.Conn, error) {
	var err error
	for port := first; port <= last; port++ {
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: ip, Port: port}}
		var c net.Conn
		if c, err = d.DialContext(ctx, network, addr); err == nil || !addrInUse(err) {
			return c, err
		}
	}
	return nil, fmt.Errorf("--local-port %d-%d: every port is in use: %w", first, last, err)
}

// sourcesFor returns the source addresses to try for addr: the first
// address of the target's family. A name target, which can resolve to
// either family, gets one address of each family (IPv4 first), unless the
// network (--ip) names one. nil ips is any address.
func sourcesFor(ips []net.IP, network, addr string) []net.IP {
	if ips == nil {
		return []net.IP{nil}
	}
	want4, want6 := network != "tcp6", network != "tcp4"
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			want4, want6 = ip.To4() != nil, ip.To4() == nil
		}
	}
	var v4, v6 net.IP
	for _, ip := range ips {
		switch {
		case ip.To4() != nil && v4 == nil:
			v4 = ip
		case ip.To4() == nil && v6 == nil:
			v6 = ip
		}
	}
	var out []net.IP
	if want4 && v4 != nil {
		out = append(out, v4)
	}
	if want6 && v6 != nil {
		out = append(out, v6)
	}
	return out
}

// interfaceIPs reads --interface: an IP address, or the name of a network
// interface, whose addresses ghola takes. "" is any address (nil).
func interfaceIPs(value string) ([]net.IP, error) {
	if value == "" {
		return nil, nil
	}
	if ip := net.ParseIP(value); ip != nil {
		return []net.IP{ip}, nil
	}
	ifc, err := net.InterfaceByName(value)
	if err != nil {
		return nil, fmt.Errorf("--interface %q: not an address or an interface: %w", value, err)
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, fmt.Errorf("--interface %q: %w", value, err)
	}
	var ips []net.IP
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipnet.IP)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("--interface %q has no address", value)
	}
	return ips, nil
}

// portRange reads --local-port: N or N-M. "" is any port (0).
func portRange(value string) (int, int, error) {
	if value == "" {
		return 0, 0, nil
	}
	lo, hi, isRange := strings.Cut(value, "-")
	first, err1 := strconv.Atoi(lo)
	last, err2 := first, error(nil)
	if isRange {
		last, err2 = strconv.Atoi(hi)
	}
	if err1 != nil || err2 != nil || first < 1 || last > 65535 || first > last {
		return 0, 0, config.BadOptionf("--local-port %q: use a port or a range such as 40000-40100", value)
	}
	return first, last, nil
}

func addrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, wsaeAddrInUse)
}
