package transport

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// socksServer is a minimal SOCKS4, SOCKS4a and SOCKS5 server for tests. It
// records how each client named the target: "name" (the proxy resolves it)
// or "ip" (the client resolved it), and the SOCKS5 user it saw.
type socksServer struct {
	ln       net.Listener
	user     string // SOCKS5 user:password required when set
	mu       sync.Mutex
	versions []int
	naming   []string
	users    []string
}

func newSocksServer(t *testing.T, user string) *socksServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksServer{ln: ln, user: user}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *socksServer) record(version int, naming, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions = append(s.versions, version)
	s.naming = append(s.naming, naming)
	s.users = append(s.users, user)
}

func (s *socksServer) last() (int, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.versions) == 0 {
		return 0, "", ""
	}
	n := len(s.versions) - 1
	return s.versions[n], s.naming[n], s.users[n]
}

// dialTarget connects to host:port. "localhost" is dialled as 127.0.0.1,
// where the test server listens.
func dialTarget(host string, port int) (net.Conn, error) {
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
}

func (s *socksServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	v, err := r.ReadByte()
	if err != nil {
		return
	}
	var target net.Conn
	switch v {
	case 4:
		target, err = s.socks4(c, r)
	case 5:
		target, err = s.socks5(c, r)
	default:
		return
	}
	if err != nil || target == nil {
		return
	}
	defer target.Close()
	go io.Copy(target, r) //nolint:errcheck
	io.Copy(c, target)    //nolint:errcheck
}

func readString(r *bufio.Reader) (string, error) {
	b, err := r.ReadBytes(0)
	if err != nil {
		return "", err
	}
	return string(b[:len(b)-1]), nil
}

func (s *socksServer) socks4(c net.Conn, r *bufio.Reader) (net.Conn, error) {
	var hdr [7]byte // command, port, IPv4
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	port := int(binary.BigEndian.Uint16(hdr[1:3]))
	ip := net.IP(hdr[3:7])
	if _, err := readString(r); err != nil { // user id
		return nil, err
	}
	host, naming := ip.String(), "ip"
	if ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] != 0 { // SOCKS4a: a name follows
		name, err := readString(r)
		if err != nil {
			return nil, err
		}
		host, naming = name, "name"
	}
	s.record(4, naming, "")
	target, err := dialTarget(host, port)
	reply := []byte{0, 90, 0, 0, 0, 0, 0, 0}
	if err != nil {
		reply[1] = 91
	}
	c.Write(reply) //nolint:errcheck
	return target, err
}

func (s *socksServer) socks5(c net.Conn, r *bufio.Reader) (net.Conn, error) {
	n, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	methods := make([]byte, n)
	if _, err := io.ReadFull(r, methods); err != nil {
		return nil, err
	}
	user := ""
	if s.user != "" {
		c.Write([]byte{5, 2}) //nolint:errcheck
		if user, err = readUserPass(r); err != nil {
			return nil, err
		}
		if user != s.user {
			c.Write([]byte{1, 1}) //nolint:errcheck
			return nil, errors.New("bad credentials")
		}
		c.Write([]byte{1, 0}) //nolint:errcheck
	} else {
		c.Write([]byte{5, 0}) //nolint:errcheck
	}
	var hdr [4]byte // version, command, reserved, address type
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	var host, naming string
	switch hdr[3] {
	case 1:
		b := make([]byte, 4)
		io.ReadFull(r, b) //nolint:errcheck
		host, naming = net.IP(b).String(), "ip"
	case 4:
		b := make([]byte, 16)
		io.ReadFull(r, b) //nolint:errcheck
		host, naming = net.IP(b).String(), "ip"
	case 3:
		l, _ := r.ReadByte()
		b := make([]byte, l)
		io.ReadFull(r, b) //nolint:errcheck
		host, naming = string(b), "name"
	}
	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return nil, err
	}
	s.record(5, naming, user)
	target, err := dialTarget(host, int(binary.BigEndian.Uint16(pb[:])))
	reply := []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	if err != nil {
		reply[1] = 5
	}
	c.Write(reply) //nolint:errcheck
	return target, err
}

func readUserPass(r *bufio.Reader) (string, error) {
	if _, err := r.ReadByte(); err != nil { // sub-negotiation version
		return "", err
	}
	read := func() (string, error) {
		l, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, l)
		_, err = io.ReadFull(r, b)
		return string(b), err
	}
	u, err := read()
	if err != nil {
		return "", err
	}
	p, err := read()
	return u + ":" + p, err
}

// Each SOCKS form that curl has works on both stacks, and names the target
// as curl does: socks4 and socks5 resolve the name in ghola, socks4a and
// socks5h send the name to the proxy. The difference matters: with socks5h
// a name that only the proxy can resolve (an internal host, a .onion name)
// works, and the client's DNS sees nothing.
func TestSOCKSProxy_EachScheme(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "through the proxy") //nolint:errcheck
	}))
	defer target.Close()
	port := target.Listener.Addr().(*net.TCPAddr).Port
	targetURL := "http://localhost:" + strconv.Itoa(port) + "/"

	cases := []struct {
		scheme  string
		version int
		naming  string
	}{
		{"socks4", 4, "ip"},
		{"socks4a", 4, "name"},
		{"socks5", 5, "ip"},
		{"socks5h", 5, "name"},
	}
	for _, imp := range stacks {
		for _, tc := range cases {
			t.Run(tc.scheme+"/impersonate="+imp, func(t *testing.T) {
				srv := newSocksServer(t, "")
				proxy := tc.scheme + "://" + srv.ln.Addr().String()
				tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, proxy)
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if err := get(t, tr, targetURL); err != nil {
					t.Fatalf("GET through %s: %v", proxy, err)
				}
				v, naming, _ := srv.last()
				if v != tc.version || naming != tc.naming {
					t.Errorf("proxy saw SOCKS%d with the target as %q; want SOCKS%d with %q", v, naming, tc.version, tc.naming)
				}
			})
		}
	}
}

// SOCKS5 credentials in the proxy URL reach the proxy (RFC 1929).
func TestSOCKSProxy_Credentials(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			srv := newSocksServer(t, "alice:s3cret")
			proxy := (&url.URL{Scheme: "socks5h", User: url.UserPassword("alice", "s3cret"), Host: srv.ln.Addr().String()}).String()
			tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, proxy)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := get(t, tr, target.URL+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			if _, _, user := srv.last(); user != "alice:s3cret" {
				t.Errorf("proxy saw user %q", user)
			}
		})
	}
}

// HTTPS through a SOCKS proxy: TLS runs over the connection that the proxy
// opened, on both stacks.
func TestSOCKSProxy_HTTPS(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "tls through the proxy") //nolint:errcheck
	}))
	defer target.Close()
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			srv := newSocksServer(t, "")
			opts := &config.Options{Stealth: config.StealthOptions{Impersonate: imp, Insecure: true}}
			tr, err := New(opts, "socks5h://"+srv.ln.Addr().String())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := get(t, tr, target.URL+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			if v, _, _ := srv.last(); v != 5 {
				t.Errorf("the request did not go through the proxy")
			}
		})
	}
}

// A SOCKS URL in the proxy variables goes through the same dialer as
// --proxy, with curl's DNS rule, on both stacks. ALL_PROXY serves a scheme
// whose own variable is not set, as in curl.
func TestSOCKSProxy_FromTheEnvironment(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	port := target.Listener.Addr().(*net.TCPAddr).Port
	targetURL := "http://localhost:" + strconv.Itoa(port) + "/"
	for _, variable := range []string{"http_proxy", "ALL_PROXY"} {
		for _, imp := range stacks {
			t.Run(variable+"/impersonate="+imp, func(t *testing.T) {
				for _, v := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"} {
					t.Setenv(v, "")
				}
				srv := newSocksServer(t, "")
				t.Setenv(variable, "socks5://"+srv.ln.Addr().String())
				tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, "")
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if err := get(t, tr, targetURL); err != nil {
					t.Fatalf("GET: %v", err)
				}
				if v, naming, _ := srv.last(); v != 5 || naming != "ip" {
					t.Errorf("proxy saw SOCKS%d with %q; want SOCKS5 with the address that ghola resolved", v, naming)
				}
			})
		}
	}
}

// --proxy wins over the environment: with an HTTP --proxy, a SOCKS
// ALL_PROXY must not carry the connection to that proxy.
func TestProxyFlag_IgnoresAnEnvironmentSOCKSProxy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	// The test proxy connects only to the test's own target, whatever the
	// request names, so it can never reach another host.
	targetAddr := target.Listener.Addr().String()
	httpProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect { // tls-client tunnels HTTP targets too
			up, err := net.Dial("tcp", targetAddr)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")) //nolint:errcheck
			go io.Copy(up, c)                                              //nolint:errcheck
			io.Copy(c, up)                                                 //nolint:errcheck
			c.Close()
			up.Close()
			return
		}
		resp, err := http.Get(target.URL + "/") // forward to the fixed target
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp.Body.Close()
	}))
	defer httpProxy.Close()
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			srv := newSocksServer(t, "")
			t.Setenv("ALL_PROXY", "socks5://"+srv.ln.Addr().String())
			tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}}, httpProxy.URL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := get(t, tr, target.URL+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			if v, _, _ := srv.last(); v != 0 {
				t.Errorf("the request went through the environment SOCKS proxy")
			}
		})
	}
}
