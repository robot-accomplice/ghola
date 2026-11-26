package proxy

import (
	"bufio"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

type Selector struct {
	proxies  []string
	strategy string
}

// rotations holds the round-robin position of each proxy list for the
// process. The client builds a new Selector for each URL, so a position
// in the Selector started at the first proxy for every URL.
var rotations sync.Map // the joined list -> *atomic.Uint64

func (s *Selector) rotation() *atomic.Uint64 {
	v, _ := rotations.LoadOrStore(strings.Join(s.proxies, " "), new(atomic.Uint64))
	return v.(*atomic.Uint64)
}

// NewSelector resolves proxy inputs into a selector.
func NewSelector(singleProxy, proxyAuth, proxyFile, strategy string) (*Selector, error) {
	if singleProxy != "" && proxyFile != "" {
		return nil, fmt.Errorf("--proxy and --proxy-file cannot be used together")
	}

	proxies := make([]string, 0, 4)
	if singleProxy != "" {
		p, err := normalize(singleProxy)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, withAuth(p, proxyAuth))
	}
	if proxyFile != "" {
		fileProxies, err := loadProxyFile(proxyFile, proxyAuth)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, fileProxies...)
	}

	return &Selector{
		proxies:  proxies,
		strategy: normalizedStrategy(strategy),
	}, nil
}

// Select chooses the proxy for the current invocation.
func (s *Selector) Select() (string, error) {
	if s == nil || len(s.proxies) == 0 {
		return "", nil
	}
	switch s.strategy {
	case "random":
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(s.proxies))))
		if err != nil {
			return "", fmt.Errorf("select random proxy: %w", err)
		}
		return s.proxies[n.Int64()], nil
	case "round-robin":
		idx := s.rotation().Add(1) - 1
		return s.proxies[idx%uint64(len(s.proxies))], nil
	default:
		return s.proxies[0], nil
	}
}

func normalizedStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "sticky":
		return "sticky"
	case "random":
		return "random"
	case "round-robin":
		return "round-robin"
	default:
		return "sticky"
	}
}

func loadProxyFile(path, proxyAuth string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var proxies []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := normalize(line)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, withAuth(p, proxyAuth))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return proxies, nil
}

// HealthCheck tests each proxy via TCP connect and removes unreachable ones.
// It logs results to stderr and returns an error only if all proxies fail.
func (s *Selector) HealthCheck() error {
	if s == nil || len(s.proxies) == 0 {
		return nil
	}

	const timeout = 5 * time.Second
	var alive []string

	for _, proxy := range s.proxies {
		u, err := url.Parse(proxy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proxy-check: a proxy line that is not a URL — parse error") // the raw line may hold a password
			continue
		}
		shown := u.Redacted() // the --proxy-user password is in the URL by now
		conn, err := net.DialTimeout("tcp", checkAddr(u), timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "proxy-check: %s — DEAD (%v)\n", shown, err)
			continue
		}
		conn.Close()
		fmt.Fprintf(os.Stderr, "proxy-check: %s — OK\n", shown)
		alive = append(alive, proxy)
	}

	if len(alive) == 0 && len(s.proxies) > 0 {
		return fmt.Errorf("all %d proxies failed health check", len(s.proxies))
	}

	s.proxies = alive
	return nil
}

func withAuth(rawProxy, proxyAuth string) string {
	if proxyAuth == "" {
		return rawProxy
	}
	u, err := url.Parse(rawProxy)
	if err != nil || u.User != nil {
		return rawProxy
	}
	// A user without ":" has an empty password: ghola cannot ask for one
	// as curl does.
	u.User = url.User(proxyAuth)
	if name, pass, ok := strings.Cut(proxyAuth, ":"); ok {
		u.User = url.UserPassword(name, pass)
	}
	return u.String()
}

// defaultProxyPort is the port of a proxy without one, as curl reads -x.
const defaultProxyPort = "1080"

// normalize reads a --proxy value or a --proxy-file line as curl reads -x:
// a value without a scheme is an HTTP proxy, on port 1080 when it has no
// port. A value with a scheme keeps the default port of its stack. A
// proxy without a host is a bad option value.
func normalize(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
		if u, err := url.Parse(raw); err == nil && u.Port() == "" && u.Hostname() != "" {
			u.Host = net.JoinHostPort(u.Hostname(), defaultProxyPort)
			raw = u.String()
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", config.BadOptionf("--proxy: not a URL: %v", redact(err))
	}
	if u.Hostname() == "" {
		return "", config.BadOptionf("--proxy %s: the proxy has no host", u.Redacted())
	}
	return raw, nil
}

// redact keeps a password out of a URL parse error, which quotes the URL.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// checkAddr is the host:port that --proxy-check connects to: the port of
// the URL, or the default port of its scheme (1080 for SOCKS, as curl).
func checkAddr(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	switch strings.ToLower(u.Scheme) {
	case "https":
		port = "443"
	case "socks4", "socks4a", "socks5", "socks5h":
		port = "1080"
	}
	return net.JoinHostPort(u.Hostname(), port)
}
