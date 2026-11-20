package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

// dohMaxAnswer limits a DoH answer: a DNS message is at most 64 KiB.
const dohMaxAnswer = 64 << 10

// ErrNoAddress marks a name that DoH did not resolve. curl exits 6 for it.
var ErrNoAddress = errors.New("could not resolve host (--doh)")

// dohResolver resolves names with DNS over HTTPS (RFC 8484) at url. It
// keeps each answer for the run, so the segments of a download ask once.
type dohResolver struct {
	url     string
	network string // "tcp4", "tcp6" or "" (--ip)
	client  *http.Client
	mu      sync.Mutex
	cache   map[string][]net.IP
}

func newDoH(url, network string) *dohResolver {
	return &dohResolver{url: url, network: network, client: &http.Client{}, cache: map[string][]net.IP{}}
}

// dial resolves the host of addr with DoH, and dials each address in turn
// with base. An IP address dials as it is.
func (r *dohResolver) dial(base dialFunc) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) != nil {
			return base(ctx, network, addr)
		}
		ips, err := r.lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			c, err := base(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func (r *dohResolver) lookup(ctx context.Context, host string) ([]net.IP, error) {
	r.mu.Lock()
	ips, ok := r.cache[host]
	r.mu.Unlock()
	if ok {
		return ips, nil
	}
	var types []dnsmessage.Type
	if r.network != "tcp6" {
		types = append(types, dnsmessage.TypeA)
	}
	if r.network != "tcp4" {
		types = append(types, dnsmessage.TypeAAAA)
	}
	for _, t := range types {
		got, err := r.query(ctx, host, t)
		if err != nil {
			return nil, err
		}
		ips = append(ips, got...)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAddress, host)
	}
	r.mu.Lock()
	r.cache[host] = ips
	r.mu.Unlock()
	return ips, nil
}

// query sends one question. RFC 8484 asks for ID 0, which caches well.
func (r *dohResolver) query(ctx context.Context, host string, t dnsmessage.Type) ([]net.IP, error) {
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		return nil, fmt.Errorf("--doh: %w", err)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: t, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	msg, err := b.Finish()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(msg))
	if err != nil {
		return nil, fmt.Errorf("--doh: %w", err)
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	rsp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("--doh: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("--doh: the server answered %s", rsp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(rsp.Body, dohMaxAnswer))
	if err != nil {
		return nil, fmt.Errorf("--doh: %w", err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(body); err != nil {
		return nil, fmt.Errorf("--doh: bad answer: %w", err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, fmt.Errorf("--doh: bad answer: %w", err)
	}
	var ips []net.IP
	for {
		h, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			return ips, nil
		}
		if err != nil {
			return nil, fmt.Errorf("--doh: bad answer: %w", err)
		}
		switch h.Type {
		case dnsmessage.TypeA:
			a, err := p.AResource()
			if err != nil {
				return nil, err
			}
			ips = append(ips, net.IP(a.A[:]))
		case dnsmessage.TypeAAAA:
			a, err := p.AAAAResource()
			if err != nil {
				return nil, err
			}
			ips = append(ips, net.IP(a.AAAA[:]))
		default:
			if err := p.SkipAnswer(); err != nil { // a CNAME: its A record follows
				return nil, err
			}
		}
	}
}
