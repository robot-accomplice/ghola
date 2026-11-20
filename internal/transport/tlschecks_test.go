package transport

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/robot-accomplice/ghola/internal/config"
)

// testPKI is a CA with a server certificate (serial 42) for 127.0.0.1.
type testPKI struct {
	ca, leaf       *x509.Certificate
	caKey, leafKey *ecdsa.PrivateKey
	caFile         string // the CA in PEM, for --tls-ca
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	p := &testPKI{}
	var err error
	if p.caKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	if p.leafKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ghola test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	p.ca = mustCert(t, caTmpl, caTmpl, &p.caKey.PublicKey, p.caKey)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	p.leaf = mustCert(t, leafTmpl, p.ca, &p.leafKey.PublicKey, p.caKey)
	p.caFile = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.ca.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustCert(t *testing.T, tmpl, parent *x509.Certificate, pub crypto.PublicKey, key crypto.Signer) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// server starts a TLS server with the leaf and the CA in its chain.
func (p *testPKI) server(t *testing.T, cfg *tls.Config) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if cfg == nil {
		cfg = &tls.Config{}
	}
	if len(cfg.Certificates) == 0 {
		cfg.Certificates = []tls.Certificate{p.cert(nil)}
	}
	ts.TLS = cfg
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func (p *testPKI) cert(staple []byte) tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{p.leaf.Raw, p.ca.Raw}, PrivateKey: p.leafKey, OCSPStaple: staple}
}

// --tls-crl refuses a certificate that the CRL lists, with exit 60, and
// accepts one that it does not list. A revoked key must not be trusted.
func TestTLS_CRL(t *testing.T) {
	p := newTestPKI(t)
	ts := p.server(t, nil)
	crl := func(serials ...int64) string {
		var entries []x509.RevocationListEntry
		for _, s := range serials {
			entries = append(entries, x509.RevocationListEntry{SerialNumber: big.NewInt(s), RevocationTime: time.Now()})
		}
		der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
			Number: big.NewInt(1), ThisUpdate: time.Now(), NextUpdate: time.Now().Add(time.Hour), RevokedCertificateEntries: entries,
		}, p.ca, p.caKey)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "crl.pem")
		if err := os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := tlsGet(t, config.StealthOptions{CACert: p.caFile, CRL: crl(7)}, ts.URL+"/"); err != nil {
		t.Errorf("a CRL without the server: %v", err)
	}
	if err := tlsGet(t, config.StealthOptions{CACert: p.caFile, CRL: crl(7, 42)}, ts.URL+"/"); !errors.Is(err, ErrCertRevoked) {
		t.Errorf("a revoked server: err = %v", err)
	}
}

// --tls-ocsp requires a staple that says "good": no staple and a
// "revoked" staple fail with exit 91.
func TestTLS_OCSP(t *testing.T) {
	p := newTestPKI(t)
	staple := func(status int) []byte {
		b, err := ocsp.CreateResponse(p.ca, p.ca, ocsp.Response{
			Status: status, SerialNumber: p.leaf.SerialNumber, ThisUpdate: time.Now(), NextUpdate: time.Now().Add(time.Hour),
			RevokedAt: time.Now(),
		}, p.caKey)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for name, tc := range map[string]struct {
		staple []byte
		ok     bool
	}{
		"good": {staple(ocsp.Good), true}, "revoked": {staple(ocsp.Revoked), false}, "no staple": {nil, false},
	} {
		ts := p.server(t, &tls.Config{Certificates: []tls.Certificate{p.cert(tc.staple)}})
		err := tlsGet(t, config.StealthOptions{CACert: p.caFile, OCSP: true}, ts.URL+"/")
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, ErrCertStatus) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// --tls-ciphers limits the TLS 1.2 suites, by IANA or OpenSSL name. Go
// picks the TLS 1.3 suites itself, so a TLS 1.3 name is an error.
func TestTLS_Ciphers(t *testing.T) {
	p := newTestPKI(t)
	ts := p.server(t, &tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384}})
	if err := tlsGet(t, config.StealthOptions{CACert: p.caFile, Ciphers: []string{"ECDHE-ECDSA-AES256-GCM-SHA384"}}, ts.URL+"/"); err != nil {
		t.Errorf("the server's suite: %v", err)
	}
	if err := tlsGet(t, config.StealthOptions{CACert: p.caFile, Ciphers: []string{"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"}}, ts.URL+"/"); err == nil {
		t.Error("a suite that the server does not have was accepted")
	}
	for _, bad := range []string{"TLS_AES_128_GCM_SHA256", "NO-SUCH-SUITE"} {
		if _, err := cipherSuites([]string{bad}); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}

// --tls-cert-type DER reads a DER certificate and key; P12 is an error.
func TestTLS_CertTypeDER(t *testing.T) {
	p := newTestPKI(t)
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	ts := p.server(t, &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool})
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.der"), filepath.Join(dir, "k.der")
	keyDER, err := x509.MarshalPKCS8PrivateKey(p.leafKey)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(certFile, p.leaf.Raw, 0o600) //nolint:errcheck
	os.WriteFile(keyFile, keyDER, 0o600)      //nolint:errcheck
	st := config.StealthOptions{CACert: p.caFile, ClientCert: certFile, ClientKey: keyFile, CertType: "DER"}
	if err := tlsGet(t, st, ts.URL+"/"); err != nil {
		t.Errorf("DER: %v", err)
	}
	st.CertType = "P12"
	if err := tlsGet(t, st, ts.URL+"/"); err == nil {
		t.Error("P12 was accepted")
	}
}
