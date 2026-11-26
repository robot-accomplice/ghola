package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The SOCKS forms that curl has. Both stacks use one dialer for them, so a
// form cannot work on one stack and not the other. Go's net/http has no
// SOCKS4, and on both stacks "socks5" sent the host name to the proxy,
// where curl resolves it in the client.
const (
	schemeSOCKS4  = "socks4"  // the client resolves the name (IPv4 only)
	schemeSOCKS4a = "socks4a" // the proxy resolves the name
	schemeSOCKS5  = "socks5"  // the client resolves the name
	schemeSOCKS5h = "socks5h" // the proxy resolves the name
)

const socksDefaultPort = "1080"

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// isSOCKS reports whether u is a SOCKS proxy URL.
func isSOCKS(u *url.URL) bool {
	switch strings.ToLower(u.Scheme) {
	case schemeSOCKS4, schemeSOCKS4a, schemeSOCKS5, schemeSOCKS5h:
		return true
	}
	return false
}

// socksDialer returns a dial function that connects to addr through the
// SOCKS proxy p. plan.direct opens the TCP connection to the proxy, and
// plan names the target: --connect-to for every form, and --resolve or
// --doh when ghola resolves the name (socks4, socks5).
func socksDialer(p *url.URL, plan *dialPlan) dialFunc {
	proxyAddr := p.Host
	if p.Port() == "" {
		proxyAddr = net.JoinHostPort(p.Hostname(), socksDefaultPort)
	}
	scheme := strings.ToLower(p.Scheme)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		addr = plan.rules.connect(addr)
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("socks: bad port %q", portStr)
		}
		hosts := []string{host}
		if scheme == schemeSOCKS4 || scheme == schemeSOCKS5 {
			if hosts, err = plan.resolve(ctx, addr, scheme == schemeSOCKS4); err != nil {
				return nil, err
			}
		}
		// Try each address in turn, as a direct connection does: a name can
		// resolve to an IPv6 address that the target does not listen on.
		var errs []error
		for _, h := range hosts {
			conn, err := socksConnect(ctx, plan.direct, proxyAddr, scheme, h, uint16(port), p.User)
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

// socksConnect opens one connection to the proxy and asks it for host:port.
func socksConnect(ctx context.Context, base dialFunc, proxyAddr, scheme, host string, port uint16, user *url.Userinfo) (net.Conn, error) {
	conn, err := base(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("socks: connect to proxy %s: %w", proxyAddr, err)
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d) //nolint:errcheck
	}
	if scheme == schemeSOCKS4 || scheme == schemeSOCKS4a {
		err = socks4Connect(conn, host, port, user.Username())
	} else {
		err = socks5Connect(conn, host, port, user)
	}
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks: %s: %w", proxyAddr, err)
	}
	conn.SetDeadline(time.Time{}) //nolint:errcheck
	return conn, nil
}

// resolve resolves the host of addr in the client, as a direct dial
// does: --resolve, else --doh or the system resolver. SOCKS4 can carry
// only IPv4.
func (p *dialPlan) resolve(ctx context.Context, addr string, v4Only bool) ([]string, error) {
	host, _, _ := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); ip != nil {
		if v4Only && ip.To4() == nil {
			return nil, fmt.Errorf("socks4: %s is not an IPv4 address", host)
		}
		return []string{host}, nil
	}
	var ips []net.IP
	for _, a := range p.rules.addresses(addr) {
		ips = append(ips, net.ParseIP(a))
	}
	if len(ips) == 0 {
		var err error
		if ips, err = p.lookup(ctx, host); err != nil {
			return nil, err
		}
	}
	var out []string
	for _, ip := range ips {
		if !v4Only || ip.To4() != nil {
			out = append(out, ip.String())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("socks: no usable address for %s", host)
	}
	return out, nil
}

// socks4Connect sends a SOCKS4 CONNECT. A host that is not an IPv4 address
// goes as a SOCKS4a name, with the address 0.0.0.1.
func socks4Connect(conn net.Conn, host string, port uint16, user string) error {
	req := []byte{4, 1, byte(port >> 8), byte(port)}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		req = append(req, 0, 0, 0, 1)
	} else {
		req = append(req, ip...)
	}
	req = append(append(req, user...), 0)
	if ip == nil {
		req = append(append(req, host...), 0)
	}
	if _, err := conn.Write(req); err != nil {
		return err
	}
	var reply [8]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[1] != 90 {
		return fmt.Errorf("socks4: the proxy refused the connection (code %d)", reply[1])
	}
	return nil
}

// socks5Connect runs the SOCKS5 handshake (RFC 1928), with user and password
// authentication (RFC 1929) when the proxy URL has credentials.
func socks5Connect(conn net.Conn, host string, port uint16, user *url.Userinfo) error {
	if err := socks5Auth(conn, user); err != nil {
		return err
	}
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 255 {
			return errors.New("socks5: host name longer than 255 bytes")
		}
		req = append(append(req, 3, byte(len(host))), host...)
	} else if ip4 := ip.To4(); ip4 != nil {
		req = append(append(req, 1), ip4...)
	} else {
		req = append(append(req, 4), ip.To16()...)
	}
	req = binary.BigEndian.AppendUint16(req, port)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	return socks5Reply(conn)
}

func socks5Auth(conn net.Conn, user *url.Userinfo) error {
	methods := []byte{5, 1, 0}
	if user != nil {
		methods = []byte{5, 2, 0, 2}
	}
	if _, err := conn.Write(methods); err != nil {
		return err
	}
	var choice [2]byte
	if _, err := io.ReadFull(conn, choice[:]); err != nil {
		return err
	}
	switch choice[1] {
	case 0:
		return nil
	case 2:
		if user == nil {
			return errors.New("socks5: the proxy needs a user and a password")
		}
		pass, _ := user.Password()
		name := user.Username()
		if len(name) > 255 || len(pass) > 255 {
			return errors.New("socks5: user or password longer than 255 bytes")
		}
		msg := append(append([]byte{1, byte(len(name))}, name...), byte(len(pass)))
		if _, err := conn.Write(append(msg, pass...)); err != nil {
			return err
		}
		var status [2]byte
		if _, err := io.ReadFull(conn, status[:]); err != nil {
			return err
		}
		if status[1] != 0 {
			return errors.New("socks5: the proxy refused the user and password")
		}
		return nil
	default:
		return errors.New("socks5: the proxy accepts no method that ghola offers")
	}
}

// socks5Reply reads the CONNECT reply and the bound address after it.
func socks5Reply(conn net.Conn) error {
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return err
	}
	if hdr[1] != 0 {
		return fmt.Errorf("socks5: the proxy refused the connection (code %d)", hdr[1])
	}
	var skip int
	switch hdr[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return err
		}
		skip = int(l[0])
	default:
		return fmt.Errorf("socks5: bad address type %d in the reply", hdr[3])
	}
	_, err := io.CopyN(io.Discard, conn, int64(skip+2))
	return err
}
