package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"net/http"
	"sync"
)

// hasProxyTLS reports a --proxy-tls-* option.
func hasProxyTLS(st config.StealthOptions) bool {
	return st.Insecure || st.CACert != "" || st.ClientCert != "" || hasTLSOptions(st)
}

// proxyTLSDial returns the dial function for an https --proxy with the
// --proxy-tls-* options: a connection to the proxy gets TLS with those
// options, and the stack then speaks HTTP to the proxy over it. Other
// addresses dial as base does. net/http alone would use the --tls-*
// options of the server for the proxy too.
func proxyTLSDial(proxyURL *url.URL, st config.StealthOptions, base dialFunc) (dialFunc, error) {
	cfg, proxyAddr, err := proxyTLSConfig(proxyURL, st)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := base(ctx, network, addr)
		if err != nil || !strings.EqualFold(addr, proxyAddr) {
			return conn, err
		}
		return proxyHandshake(ctx, conn, cfg)
	}, nil
}

// proxyTLSConfig returns the TLS config for an https proxy, built from the
// --proxy-tls-* options only, and the proxy's host:port.
func proxyTLSConfig(proxyURL *url.URL, st config.StealthOptions) (*tls.Config, string, error) {
	cfg, err := buildTLSConfig(&config.Options{Stealth: st})
	if err != nil {
		return nil, "", fmt.Errorf("--proxy-tls: %w", err)
	}
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg = cfg.Clone()
	cfg.ServerName = proxyURL.Hostname()
	cfg.NextProtos = []string{"http/1.1"}
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), "443")
	}
	return cfg, proxyAddr, nil
}

func proxyHandshake(ctx context.Context, conn net.Conn, cfg *tls.Config) (net.Conn, error) {
	tc := tls.Client(conn, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("TLS to the proxy: %w", err)
	}
	return tc, nil
}

// envProxyTLS gives an https proxy from the environment its own TLS, as
// proxyTLSDial does for --proxy: net/http alone would use the server's
// -k and --tls-ca for it. proxy hands net/http the proxy as http://, and
// records its address; dial then adds TLS when it connects there.
type envProxyTLS struct {
	st    config.StealthOptions
	mu    sync.Mutex
	hosts map[string]*tls.Config // proxy host:port -> its TLS config
}

func (e *envProxyTLS) proxy(next func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		p, err := next(r)
		if err != nil || p == nil || !strings.EqualFold(p.Scheme, "https") {
			return p, err
		}
		cfg, addr, err := proxyTLSConfig(p, e.st)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		if e.hosts == nil {
			e.hosts = map[string]*tls.Config{}
		}
		e.hosts[strings.ToLower(addr)] = cfg
		e.mu.Unlock()
		plain := *p
		plain.Scheme = "http" // dial adds the TLS
		plain.Host = addr
		return &plain, nil
	}
}

func (e *envProxyTLS) dial(next dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := next(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		e.mu.Lock()
		cfg := e.hosts[strings.ToLower(addr)]
		e.mu.Unlock()
		if cfg == nil {
			return conn, nil
		}
		return proxyHandshake(ctx, conn, cfg)
	}
}
