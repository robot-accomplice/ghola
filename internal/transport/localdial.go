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

// localDial returns the base dial function: from the --interface address
// and a --local-port in its range, or to the --unix-socket. The socket
// serves HTTP only, as in curl, so FTP and SSH pass unixOK false.
func localDial(n config.NetOptions, unixOK bool) (dialFunc, error) {
	d, err := sourceDial(n, unixOK)
	if err != nil || n.DoH == "" || (n.UnixSocket != "" && unixOK) {
		return d, err
	}
	network := ""
	if n.IPVersion == "4" || n.IPVersion == "6" {
		network = "tcp" + n.IPVersion
	}
	return newDoH(n.DoH, network).dial(d), nil // --doh resolves the name
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
	ip, err := interfaceIP(n.Interface)
	if err != nil {
		return nil, err
	}
	first, last, err := portRange(n.LocalPort)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var err error
		for port := first; port <= last; port++ {
			d := net.Dialer{LocalAddr: &net.TCPAddr{IP: ip, Port: port}}
			var c net.Conn
			if c, err = d.DialContext(ctx, network, addr); err == nil || !addrInUse(err) {
				return c, err
			}
		}
		return nil, fmt.Errorf("--local-port %s: every port is in use: %w", n.LocalPort, err)
	}, nil
}

// interfaceIP reads --interface: an IP address, or the name of a network
// interface, whose first address ghola takes. "" is any address.
func interfaceIP(value string) (net.IP, error) {
	if value == "" {
		return nil, nil
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip, nil
	}
	ifc, err := net.InterfaceByName(value)
	if err != nil {
		return nil, fmt.Errorf("--interface %q: not an address or an interface: %w", value, err)
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, fmt.Errorf("--interface %q: %w", value, err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			return ipnet.IP, nil
		}
	}
	return nil, fmt.Errorf("--interface %q has no address", value)
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
		return 0, 0, fmt.Errorf("--local-port %q: use a port or a range such as 40000-40100", value)
	}
	return first, last, nil
}

func addrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, wsaeAddrInUse)
}
