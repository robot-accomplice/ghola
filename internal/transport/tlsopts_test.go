package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

func tlsServer(t *testing.T, maxVersion uint16) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.TLS = &tls.Config{MaxVersion: maxVersion}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func tlsGet(t *testing.T, st config.StealthOptions, url string) error {
	t.Helper()
	tr, err := New(&config.Options{Stealth: st}, "")
	if err != nil {
		return err
	}
	return get(t, tr, url)
}

// --tls-min 1.3 refuses a server that speaks only TLS 1.2; --tls-max 1.2
// works with it.
func TestTLS_Versions(t *testing.T) {
	ts := tlsServer(t, tls.VersionTLS12)
	if err := tlsGet(t, config.StealthOptions{Insecure: true, TLSMin: "1.3"}, ts.URL+"/"); err == nil {
		t.Error("--tls-min 1.3 reached a TLS 1.2 server")
	}
	if err := tlsGet(t, config.StealthOptions{Insecure: true, TLSMax: "1.2"}, ts.URL+"/"); err != nil {
		t.Errorf("--tls-max 1.2: %v", err)
	}
}

// --tls-pin accepts the server's key hash and refuses another (exit 90),
// also with -k, as curl checks the pin then too.
func TestTLS_Pin(t *testing.T) {
	ts := tlsServer(t, 0)
	sum := sha256.Sum256(ts.Certificate().RawSubjectPublicKeyInfo)
	good := "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
	bad := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := tlsGet(t, config.StealthOptions{Insecure: true, Pins: []string{bad + ";" + good}}, ts.URL+"/"); err != nil {
		t.Errorf("the right pin: %v", err)
	}
	if err := tlsGet(t, config.StealthOptions{Insecure: true, Pins: []string{bad}}, ts.URL+"/"); !errors.Is(err, ErrPinMismatch) {
		t.Errorf("a wrong pin: err = %v", err)
	}
	if err := tlsGet(t, config.StealthOptions{Pins: []string{"md5//x"}}, ts.URL+"/"); err == nil {
		t.Error("a pin in the wrong format was accepted")
	}
}

// --tls-ca-dir trusts the certificates in a folder, without -k.
func TestTLS_CADir(t *testing.T) {
	ts := tlsServer(t, 0)
	dir := t.TempDir()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
	os.WriteFile(filepath.Join(dir, "server.pem"), pemBytes, 0o644) //nolint:errcheck
	if err := tlsGet(t, config.StealthOptions{CADir: dir}, ts.URL+"/"); err != nil {
		t.Errorf("--tls-ca-dir: %v", err)
	}
	if err := tlsGet(t, config.StealthOptions{}, ts.URL+"/"); err == nil {
		t.Error("the test server is trusted without the folder: the test proves nothing")
	}
}

// With --impersonate, the profile sets the handshake: the TLS options are
// an error, not dropped.
func TestTLS_ImpersonateRefuses(t *testing.T) {
	for _, st := range []config.StealthOptions{{TLSMin: "1.3"}, {CADir: "x"}, {Pins: []string{"sha256//x"}}} {
		st.Impersonate = "chrome"
		if _, err := New(&config.Options{Stealth: st}, ""); err == nil {
			t.Errorf("%+v was accepted with --impersonate", st)
		}
	}
}
