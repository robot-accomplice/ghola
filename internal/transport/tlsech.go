package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ErrECHNeedsDoH refuses --tls-ech without --doh: the ECH config comes
// from the HTTPS record of the host, which Go's resolver does not read.
var ErrECHNeedsDoH = errors.New("--tls-ech needs --doh: ghola reads the ECH config from the host's HTTPS record")

// echConfig returns the ECH config list of the host's HTTPS record, or
// nil when the host offers none.
func (r *dohResolver) echConfig(ctx context.Context, host string) ([]byte, error) {
	r.mu.Lock()
	list, ok := r.ech[host]
	r.mu.Unlock()
	if ok {
		return list, nil
	}
	var found []byte
	err := r.ask(ctx, host, dnsmessage.TypeHTTPS, func(p *dnsmessage.Parser, h dnsmessage.ResourceHeader) error {
		if h.Type != dnsmessage.TypeHTTPS {
			return p.SkipAnswer()
		}
		rr, err := p.HTTPSResource()
		if err != nil {
			return err
		}
		if v, ok := rr.GetParam(dnsmessage.SVCParamECH); ok && found == nil {
			found = v
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.ech[host] = found
	r.mu.Unlock()
	return found, nil
}

// useECH sets the net/http transport for --tls-ech: each TLS handshake
// gets the ECH config of its host, so the server name is encrypted. A
// host without ECH gets plain TLS. Go does its own TLS through a proxy,
// which would drop ECH without a sign, so a proxy is an error.
func useECH(tr *http.Transport, opts *config.Options, base dialFunc) error {
	if !opts.Stealth.ECH {
		return nil
	}
	if opts.Net.DoH == "" {
		return ErrECHNeedsDoH
	}
	r, err := newDoH(opts.Net.DoH, "", opts.Stealth)
	if err != nil {
		return err
	}
	cfg := tr.TLSClientConfig
	if cfg == nil {
		cfg = &tls.Config{}
	}
	proxy := tr.Proxy
	tr.Proxy = func(req *http.Request) (*url.URL, error) {
		if proxy == nil {
			return nil, nil
		}
		p, err := proxy(req)
		if err == nil && p != nil {
			return nil, fmt.Errorf("--tls-ech does not work through a proxy (%s)", p.Redacted())
		}
		return p, err
	}
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host := hostOnly(addr)
		list, err := r.echConfig(ctx, host)
		if err != nil {
			return nil, err
		}
		c := cfg.Clone()
		c.ServerName, c.NextProtos = host, []string{"h2", "http/1.1"}
		c.EncryptedClientHelloConfigList = list
		conn, err := base(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(conn, c)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
	return nil
}
