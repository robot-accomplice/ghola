package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A "good" staple that expired, or that is not valid yet, says nothing
// about the certificate now: a server could replay an old answer from
// before the revocation. --tls-ocsp must refuse it (exit 91).
func TestTLS_OCSPRefusesAStapleOutsideItsValidityTimes(t *testing.T) {
	p := newTestPKI(t)
	now := time.Now()
	staple := func(this, next time.Time) []byte {
		b, err := ocsp.CreateResponse(p.ca, p.ca, ocsp.Response{
			Status: ocsp.Good, SerialNumber: p.leaf.SerialNumber, ThisUpdate: this, NextUpdate: next,
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
		"valid now":     {staple(now.Add(-time.Hour), now.Add(time.Hour)), true},
		"expired":       {staple(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), false},
		"not valid yet": {staple(now.Add(24*time.Hour), now.Add(48*time.Hour)), false},
	} {
		ts := p.server(t, &tls.Config{Certificates: []tls.Certificate{p.cert(tc.staple)}})
		err := tlsGet(t, config.StealthOptions{CACert: p.caFile, OCSP: true}, ts.URL+"/")
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, ErrCertStatus) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// curl takes several pins split by ";", and users copy lists split by ","
// too. A comma list must not be one bad pin.
func TestTLS_PinTakesACommaList(t *testing.T) {
	ts := tlsServer(t, 0)
	sum := sha256.Sum256(ts.Certificate().RawSubjectPublicKeyInfo)
	good := "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
	bad := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := tlsGet(t, config.StealthOptions{Insecure: true, Pins: []string{bad + "," + good}}, ts.URL+"/"); err != nil {
		t.Errorf("a comma list with the right pin: %v", err)
	}
}

// A pin that is not sha256//BASE64 is a bad option value: exit 2, not a
// transfer error.
func TestTLS_BadPinIsABadOption(t *testing.T) {
	_, err := New(&config.Options{Stealth: config.StealthOptions{Pins: []string{"md5//x"}}}, "")
	if !errors.Is(err, config.ErrBadOption) {
		t.Errorf("err = %v, want config.ErrBadOption", err)
	}
}

// A wrong pin over ECH is still a pin mismatch (exit 90), not a receive
// error: the ECH dial must not hide the cause of a failed handshake.
func TestTLS_ECHWithAWrongPinIsAPinMismatch(t *testing.T) {
	cfg, key := echKey(t, "public.invalid")
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.TLS = &tls.Config{EncryptedClientHelloKeys: []tls.EncryptedClientHelloKey{{Config: cfg, PrivateKey: key}}}
	ts.StartTLS()
	defer ts.Close()
	list := binary.BigEndian.AppendUint16(nil, uint16(len(cfg)))
	doh := echDoH(t, append(list, cfg...))
	bad := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	tr, err := New(&config.Options{Stealth: config.StealthOptions{ECH: true, Insecure: true, Pins: []string{bad}}, Net: config.NetOptions{DoH: doh.URL}}, "")
	if err != nil {
		t.Fatal(err)
	}
	err = get(t, tr, "https://ghola-test.invalid:"+port(ts)+"/")
	if !errors.Is(err, ErrPinMismatch) || errors.Is(err, ErrRecv) {
		t.Errorf("err = %v, want ErrPinMismatch and not ErrRecv", err)
	}
}
