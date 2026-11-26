package transport

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/robot-accomplice/ghola/internal/config"
)

var (
	// ErrCertRevoked marks a server certificate in the --tls-crl list.
	// curl exits 60 for it, as for another certificate problem.
	ErrCertRevoked = errors.New("the server certificate is revoked (--tls-crl)")
	// ErrCertStatus marks a missing or bad OCSP staple under --tls-ocsp.
	// curl --cert-status exits 91 for it.
	ErrCertStatus = errors.New("the server sent no good OCSP staple (--tls-ocsp)")
)

// connCheck is one check of the TLS connection after the handshake.
type connCheck func(tls.ConnectionState) error

// connectionChecks returns the checks of --tls-pin, --tls-crl and
// --tls-ocsp, or nil without them. They run also with -k, as curl checks
// a pin then too.
func connectionChecks(st config.StealthOptions) (func(tls.ConnectionState) error, error) {
	var checks []connCheck
	if len(st.Pins) > 0 {
		pins, err := parsePins(st.Pins)
		if err != nil {
			return nil, err
		}
		checks = append(checks, pins.verify)
	}
	if st.CRL != "" {
		crl, err := readCRL(st.CRL)
		if err != nil {
			return nil, err
		}
		checks = append(checks, crl.verify)
	}
	if st.OCSP {
		checks = append(checks, verifyStaple)
	}
	if len(checks) == 0 {
		return nil, nil
	}
	return func(cs tls.ConnectionState) error {
		for _, c := range checks {
			if err := c(cs); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// revocations is a CRL: the issuer and the revoked serial numbers.
type revocations struct {
	issuer  []byte
	serials map[string]bool
}

// readCRL reads a CRL file in PEM or DER.
func readCRL(path string) (*revocations, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--tls-crl: %w", err)
	}
	if block, _ := pem.Decode(b); block != nil {
		b = block.Bytes
	}
	list, err := x509.ParseRevocationList(b)
	if err != nil {
		return nil, fmt.Errorf("--tls-crl %q: %w", path, err)
	}
	r := &revocations{issuer: list.RawIssuer, serials: map[string]bool{}}
	for _, e := range list.RevokedCertificateEntries {
		r.serials[e.SerialNumber.String()] = true
	}
	return r, nil
}

// verify refuses a certificate of the chain that the CRL's issuer revoked.
func (r *revocations) verify(cs tls.ConnectionState) error {
	for _, c := range cs.PeerCertificates {
		if bytes.Equal(c.RawIssuer, r.issuer) && r.serials[c.SerialNumber.String()] {
			return fmt.Errorf("%w: serial %s", ErrCertRevoked, c.SerialNumber)
		}
	}
	return nil
}

// verifyStaple requires a stapled OCSP answer that says the server
// certificate is good. Go asks for the staple in each handshake.
func verifyStaple(cs tls.ConnectionState) error {
	if len(cs.OCSPResponse) == 0 || len(cs.PeerCertificates) == 0 {
		return ErrCertStatus
	}
	leaf := cs.PeerCertificates[0]
	var issuer *x509.Certificate
	switch {
	case len(cs.VerifiedChains) > 0 && len(cs.VerifiedChains[0]) > 1:
		issuer = cs.VerifiedChains[0][1]
	case len(cs.PeerCertificates) > 1:
		issuer = cs.PeerCertificates[1]
	default:
		return fmt.Errorf("%w: no issuer certificate to check the answer", ErrCertStatus)
	}
	rsp, err := ocsp.ParseResponseForCert(cs.OCSPResponse, leaf, issuer)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCertStatus, err)
	}
	if rsp.Status != ocsp.Good {
		return fmt.Errorf("%w: the status is %s", ErrCertStatus, ocspStatus[rsp.Status])
	}
	return stapleCurrent(rsp, time.Now())
}

// ocspClockSkew is the clock difference that the validity check of a
// staple allows, as OpenSSL's OCSP_check_validity does for curl.
const ocspClockSkew = 5 * time.Minute

// stapleCurrent refuses an answer that is not valid at now: an old "good"
// answer from before a revocation says nothing about the certificate now.
func stapleCurrent(rsp *ocsp.Response, now time.Time) error {
	if now.Add(ocspClockSkew).Before(rsp.ThisUpdate) {
		return fmt.Errorf("%w: the answer is valid only from %s", ErrCertStatus, rsp.ThisUpdate.UTC().Format(time.RFC3339))
	}
	if !rsp.NextUpdate.IsZero() && now.Add(-ocspClockSkew).After(rsp.NextUpdate) {
		return fmt.Errorf("%w: the answer expired at %s", ErrCertStatus, rsp.NextUpdate.UTC().Format(time.RFC3339))
	}
	return nil
}

var ocspStatus = map[int]string{ocsp.Good: "good", ocsp.Revoked: "revoked", ocsp.Unknown: "unknown"}

// openSSLCiphers are the OpenSSL names of the modern suites, which curl
// --ciphers takes. Other suites take their IANA name.
var openSSLCiphers = map[string]string{
	"ECDHE-ECDSA-AES128-GCM-SHA256": "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
	"ECDHE-RSA-AES128-GCM-SHA256":   "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	"ECDHE-ECDSA-AES256-GCM-SHA384": "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
	"ECDHE-RSA-AES256-GCM-SHA384":   "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
	"ECDHE-ECDSA-CHACHA20-POLY1305": "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256",
	"ECDHE-RSA-CHACHA20-POLY1305":   "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
}

// cipherSuites reads --tls-ciphers: names split by ":" or ",". Go picks
// the TLS 1.3 suites itself, so a TLS 1.3 name is an error.
func cipherSuites(values []string) ([]uint16, error) {
	suites := map[string]*tls.CipherSuite{}
	for _, s := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		suites[s.Name] = s
	}
	var out []uint16
	for _, v := range values {
		for _, name := range strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ',' }) {
			name = strings.TrimSpace(name)
			if iana, ok := openSSLCiphers[name]; ok {
				name = iana
			}
			s, ok := suites[name]
			switch {
			case !ok:
				return nil, config.BadOptionf("--tls-ciphers %q: not a cipher suite; use the IANA name, such as TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", name)
			case onlyTLS13(s):
				return nil, config.BadOptionf("--tls-ciphers %s: Go picks the TLS 1.3 suites itself", name)
			}
			out = append(out, s.ID)
		}
	}
	return out, nil
}

func onlyTLS13(s *tls.CipherSuite) bool {
	for _, v := range s.SupportedVersions {
		if v != tls.VersionTLS13 {
			return false
		}
	}
	return true
}

// loadClientCert reads --tls-cert and --tls-key in the --tls-cert-type
// format: PEM (the default) or DER.
func loadClientCert(st config.StealthOptions) (tls.Certificate, error) {
	if st.ClientKey == "" {
		return tls.Certificate{}, config.BadOptionf("--tls-cert needs --tls-key")
	}
	switch strings.ToUpper(st.CertType) {
	case "", "PEM":
		cert, err := tls.LoadX509KeyPair(st.ClientCert, st.ClientKey)
		if err != nil {
			return cert, fmt.Errorf("load client cert/key: %w", err)
		}
		return cert, nil
	case "DER":
		return loadDERPair(st.ClientCert, st.ClientKey)
	default:
		// curl takes the P12 password in --cert FILE:PASSWORD; the v1 spec
		// has no place for it yet.
		return tls.Certificate{}, config.BadOptionf("--tls-cert-type %s: use PEM or DER", st.CertType)
	}
}

func loadDERPair(certFile, keyFile string) (tls.Certificate, error) {
	certDER, err := os.ReadFile(certFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := os.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("--tls-cert (DER): %w", err)
	}
	key, err := parseDERKey(keyDER)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("--tls-key (DER): %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key, Leaf: leaf}, nil
}

// parseDERKey reads a PKCS #8, PKCS #1 or EC private key.
func parseDERKey(der []byte) (any, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	return x509.ParseECPrivateKey(der)
}
