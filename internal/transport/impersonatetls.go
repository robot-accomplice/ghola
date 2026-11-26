package transport

import (
	"crypto/tls"

	tlsclient "github.com/bogdanfinn/tls-client"
	utls "github.com/bogdanfinn/utls"

	"github.com/robot-accomplice/ghola/internal/config"
)

// impersonateRefused checks the --tls-* options that cannot work with
// --impersonate, and returns an ErrBadOption (exit 2) for the first one.
// The browser profile sets the versions, the cipher suites, the OCSP staple
// request and the ECH extension of the handshake: an option that changed
// them would change the fingerprint that --impersonate copies. HTTP/3
// dials QUIC past the hook that checks --tls-pin and --tls-crl.
func impersonateRefused(opts *config.Options) error {
	st := opts.Stealth
	for _, o := range []struct {
		name string
		set  bool
	}{
		{"--tls-min", st.TLSMin != ""}, {"--tls-max", st.TLSMax != ""}, {"--tls-ciphers", len(st.Ciphers) > 0},
		{"--tls-ocsp", st.OCSP}, {"--tls-ech", st.ECH},
	} {
		if o.set {
			return config.BadOptionf("%s does not work with --impersonate: the browser profile sets the TLS handshake", o.name)
		}
	}
	if hasProxyTLS(opts.ProxyCfg.TLS) {
		return config.BadOptionf("the --proxy-tls-* options do not work with --impersonate")
	}
	if st.HTTP3 && (len(st.Pins) > 0 || st.CRL != "") {
		return config.BadOptionf("--tls-pin and --tls-crl do not work with --http 3 and --impersonate: HTTP/3 does not check them")
	}
	return nil
}

// impersonateTLS returns the tls-client options of the --tls-* options that
// work with --impersonate: --tls-ca, --tls-ca-dir, --tls-cert with
// --tls-key (and --tls-cert-type), --tls-pin and --tls-crl. They change
// what ghola trusts and the certificate it sends, not the ClientHello.
func impersonateTLS(st config.StealthOptions) ([]tlsclient.HttpClientOption, *tlsclient.TransportOptions, error) {
	var tlsOpts tlsclient.TransportOptions
	pool, err := caPool(st.CACert, st.CADir)
	if err != nil {
		return nil, nil, err
	}
	tlsOpts.RootCAs = pool
	if st.ClientCert != "" {
		cert, err := loadClientCert(st)
		if err != nil {
			return nil, nil, err
		}
		tlsOpts.Certificates = []utls.Certificate{toUTLS(cert)}
	}
	checks, err := connectionChecks(st)
	if err != nil {
		return nil, nil, err
	}
	// tls-client calls the pinner after each TLS handshake, on HTTP/1.1,
	// HTTP/2 and WebSocket connections. It replaces tls-client's own pin
	// store, which no --tls-* option fills.
	options := []tlsclient.HttpClientOption{tlsclient.WithCertificatePinner(connPinner{checks})}
	return options, &tlsOpts, nil
}

// toUTLS copies a crypto/tls client certificate into the utls type.
func toUTLS(c tls.Certificate) utls.Certificate {
	return utls.Certificate{Certificate: c.Certificate, PrivateKey: c.PrivateKey, Leaf: c.Leaf}
}

// connPinner runs the connection checks of --tls-pin and --tls-crl on a
// utls connection. nil checks accept every connection.
type connPinner struct {
	checks func(tls.ConnectionState) error
}

func (p connPinner) Pin(conn *utls.UConn, _ string) error {
	if p.checks == nil {
		return nil
	}
	cs := conn.ConnectionState()
	return p.checks(tls.ConnectionState{
		PeerCertificates: cs.PeerCertificates,
		VerifiedChains:   cs.VerifiedChains,
		OCSPResponse:     cs.OCSPResponse,
	})
}
