package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
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
	cfg, err := buildTLSConfig(&config.Options{Stealth: st})
	if err != nil {
		return nil, fmt.Errorf("--proxy-tls: %w", err)
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
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := base(ctx, network, addr)
		if err != nil || !strings.EqualFold(addr, proxyAddr) {
			return conn, err
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS to the proxy: %w", err)
		}
		return tc, nil
	}, nil
}
