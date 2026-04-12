package proxy

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type Selector struct {
	proxies   []string
	strategy  string
	lastIndex atomic.Uint64
}

// NewSelector resolves proxy inputs into a selector.
func NewSelector(singleProxy, proxyAuth, proxyFile, strategy string) (*Selector, error) {
	if singleProxy != "" && proxyFile != "" {
		return nil, fmt.Errorf("--proxy and --proxy-file cannot be used together")
	}

	proxies := make([]string, 0, 4)
	if singleProxy != "" {
		proxies = append(proxies, withAuth(singleProxy, proxyAuth))
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
		idx := s.lastIndex.Add(1) - 1
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
		proxies = append(proxies, withAuth(line, proxyAuth))
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
			fmt.Fprintf(os.Stderr, "proxy-check: %s — parse error: %v\n", proxy, err)
			continue
		}
		host := u.Host
		if !strings.Contains(host, ":") {
			if u.Scheme == "https" {
				host += ":443"
			} else {
				host += ":80"
			}
		}
		conn, err := net.DialTimeout("tcp", host, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "proxy-check: %s — DEAD (%v)\n", proxy, err)
			continue
		}
		conn.Close()
		fmt.Fprintf(os.Stderr, "proxy-check: %s — OK\n", proxy)
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
	parts := strings.SplitN(proxyAuth, ":", 2)
	if len(parts) != 2 {
		return rawProxy
	}
	u.User = url.UserPassword(parts[0], parts[1])
	return u.String()
}
