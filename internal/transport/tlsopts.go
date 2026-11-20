package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
)

// ErrPinMismatch marks a server key that does not match --tls-pin. curl
// exits 90 for it.
var ErrPinMismatch = errors.New("the server public key does not match --tls-pin")

// tlsVersions are the values of --tls-min and --tls-max.
var tlsVersions = map[string]uint16{"1.2": tls.VersionTLS12, "1.3": tls.VersionTLS13}

func hasTLSOptions(st config.StealthOptions) bool {
	return st.TLSMin != "" || st.TLSMax != "" || st.CADir != "" || len(st.Pins) > 0 ||
		len(st.Ciphers) > 0 || st.CRL != "" || st.OCSP || st.CertType != ""
}

func setTLSVersions(cfg *tls.Config, lowest, highest string) error {
	for _, v := range []struct {
		name, value string
		field       *uint16
	}{{"--tls-min", lowest, &cfg.MinVersion}, {"--tls-max", highest, &cfg.MaxVersion}} {
		if v.value == "" {
			continue
		}
		ver, ok := tlsVersions[v.value]
		if !ok {
			return fmt.Errorf("%s %q: use 1.2 or 1.3", v.name, v.value)
		}
		*v.field = ver
	}
	if cfg.MinVersion != 0 && cfg.MaxVersion != 0 && cfg.MinVersion > cfg.MaxVersion {
		return fmt.Errorf("--tls-min %s is above --tls-max %s", lowest, highest)
	}
	return nil
}

// caPool reads --tls-ca (a PEM file) and --tls-ca-dir (each PEM file in
// the folder). With neither, it returns nil: the system store.
func caPool(file, dir string) (*x509.CertPool, error) {
	if file == "" && dir == "" {
		return nil, nil
	}
	pool := x509.NewCertPool()
	var files []string
	if file != "" {
		files = append(files, file)
	}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("--tls-ca-dir: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	added := 0
	for _, f := range files {
		pem, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		if pool.AppendCertsFromPEM(pem) {
			added++
		} else if f == file {
			return nil, fmt.Errorf("--tls-ca %q contains no valid certificates", file)
		}
	}
	if added == 0 {
		return nil, fmt.Errorf("--tls-ca-dir %q contains no valid certificates", dir)
	}
	return pool, nil
}

// pinSet holds the sha256 hashes of --tls-pin.
type pinSet map[string]bool

// parsePins reads sha256//BASE64 values; several may be joined with ";",
// as curl takes them.
func parsePins(values []string) (pinSet, error) {
	pins := pinSet{}
	for _, v := range values {
		for _, p := range strings.Split(v, ";") {
			b64, ok := strings.CutPrefix(strings.TrimSpace(p), "sha256//")
			raw, err := base64.StdEncoding.DecodeString(b64)
			if !ok || err != nil || len(raw) != sha256.Size {
				return nil, fmt.Errorf("--tls-pin %q: use sha256//BASE64 of the public key", p)
			}
			pins[b64] = true
		}
	}
	return pins, nil
}

// verify accepts the connection when the leaf public key matches a pin.
// It runs also with -k, as curl checks the pin then too.
func (p pinSet) verify(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return ErrPinMismatch
	}
	sum := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
	if p[base64.StdEncoding.EncodeToString(sum[:])] {
		return nil
	}
	return ErrPinMismatch
}
