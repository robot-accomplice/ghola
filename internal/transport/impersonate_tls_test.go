package transport

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// impersonateGet is tlsGet on the --impersonate stack.
func impersonateGet(t *testing.T, st config.StealthOptions, url string) error {
	t.Helper()
	st.Impersonate = "chrome"
	return tlsGet(t, st, url)
}

// --tls-ca and --tls-ca-dir must reach the --impersonate stack: a private
// CA is the normal case for an internal server, and v1.0.0 refused them.
// The server must not be trusted without the CA, or the test proves nothing.
func TestImpersonate_TrustsTheGivenCA(t *testing.T) {
	p := newTestPKI(t)
	ts := p.server(t, nil)
	if err := impersonateGet(t, config.StealthOptions{}, ts.URL+"/"); err == nil {
		t.Fatal("the test server is trusted without --tls-ca: the test proves nothing")
	}
	if err := impersonateGet(t, config.StealthOptions{CACert: p.caFile}, ts.URL+"/"); err != nil {
		t.Errorf("--tls-ca: %v", err)
	}
	dir := filepath.Dir(p.caFile)
	if err := impersonateGet(t, config.StealthOptions{CADir: dir}, ts.URL+"/"); err != nil {
		t.Errorf("--tls-ca-dir: %v", err)
	}
}

// --tls-cert and --tls-key must send the client certificate on the
// --impersonate stack, or a server that requires one refuses the request.
func TestImpersonate_SendsTheClientCertificate(t *testing.T) {
	p := newTestPKI(t)
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	ts := p.server(t, &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool})
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	keyDER, err := x509.MarshalPKCS8PrivateKey(p.leafKey)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.leaf.Raw}), 0o600) //nolint:errcheck
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)      //nolint:errcheck
	if err := impersonateGet(t, config.StealthOptions{CACert: p.caFile}, ts.URL+"/"); err == nil {
		t.Fatal("the server accepted a request without a client certificate: the test proves nothing")
	}
	st := config.StealthOptions{CACert: p.caFile, ClientCert: certFile, ClientKey: keyFile}
	if err := impersonateGet(t, st, ts.URL+"/"); err != nil {
		t.Errorf("--tls-cert: %v", err)
	}
}

// --tls-pin must check the server key on the --impersonate stack, also
// with -k, and a wrong pin must be ErrPinMismatch (exit 90).
func TestImpersonate_ChecksThePin(t *testing.T) {
	p := newTestPKI(t)
	ts := p.server(t, nil)
	sum := sha256.Sum256(p.leaf.RawSubjectPublicKeyInfo)
	good := "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
	bad := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := impersonateGet(t, config.StealthOptions{CACert: p.caFile, Pins: []string{good}}, ts.URL+"/"); err != nil {
		t.Errorf("the right pin: %v", err)
	}
	if err := impersonateGet(t, config.StealthOptions{Insecure: true, Pins: []string{bad}}, ts.URL+"/"); !errors.Is(err, ErrPinMismatch) {
		t.Errorf("a wrong pin with -k: err = %v, want ErrPinMismatch", err)
	}
}

// --tls-crl must refuse a revoked server on the --impersonate stack
// (exit 60), and accept one that the list does not name.
func TestImpersonate_ChecksTheCRL(t *testing.T) {
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
		f := filepath.Join(t.TempDir(), "crl.der")
		if err := os.WriteFile(f, der, 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if err := impersonateGet(t, config.StealthOptions{CACert: p.caFile, CRL: crl(7)}, ts.URL+"/"); err != nil {
		t.Errorf("a CRL without the server: %v", err)
	}
	if err := impersonateGet(t, config.StealthOptions{CACert: p.caFile, CRL: crl(42)}, ts.URL+"/"); !errors.Is(err, ErrCertRevoked) {
		t.Errorf("a revoked server: err = %v, want ErrCertRevoked", err)
	}
}

// The browser profile sets the versions, the suites, the staple request
// and ECH, so these options cannot apply. They must be an argument error
// (exit 2) that names --impersonate, not a silent change of the
// fingerprint and not a transfer error.
func TestImpersonate_RefusesTheHandshakeOptionsAsBadOptions(t *testing.T) {
	for name, st := range map[string]config.StealthOptions{
		"--tls-min": {TLSMin: "1.3"}, "--tls-max": {TLSMax: "1.2"}, "--tls-ciphers": {Ciphers: []string{"ECDHE-RSA-AES128-GCM-SHA256"}},
		"--tls-ocsp": {OCSP: true}, "--tls-ech": {ECH: true},
	} {
		st.Impersonate = "chrome"
		_, err := New(&config.Options{Stealth: st}, "")
		if !errors.Is(err, config.ErrBadOption) {
			t.Errorf("%s: err = %v, want config.ErrBadOption", name, err)
		}
	}
}

// HTTP/3 dials QUIC past the hook that checks the pin and the CRL, so the
// two together must be an argument error and not an unchecked connection.
func TestImpersonate_RefusesPinOrCRLWithHTTP3(t *testing.T) {
	for name, st := range map[string]config.StealthOptions{
		"--tls-pin": {Pins: []string{"sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))}},
		"--tls-crl": {CRL: "x.crl"},
	} {
		st.Impersonate, st.HTTP3 = "chrome", true
		_, err := New(&config.Options{Stealth: st}, "")
		if !errors.Is(err, config.ErrBadOption) {
			t.Errorf("%s with --http 3: err = %v, want config.ErrBadOption", name, err)
		}
	}
}

// With --tls-pin or --tls-crl, an Alt-Svc h3 entry must not move the
// transfer to HTTP/3, where the checks do not run, and must not fail the
// whole run either: the transfer stays on TCP.
func TestAltSvc_StaysOnTCPWithPinOrCRL(t *testing.T) {
	for name, st := range map[string]config.StealthOptions{
		"--tls-pin": {Pins: []string{"sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))}},
		"--tls-crl": {CRL: "x.crl"},
	} {
		st.Impersonate = "chrome"
		h3, err := newH3Network(&config.Options{Stealth: st}, "")
		if err != nil || h3 != nil {
			t.Errorf("%s: %v %v, want no HTTP/3 stack and no error", name, h3, err)
		}
	}
}
