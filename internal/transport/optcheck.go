package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
)

// CheckOptions runs the value checks of the TLS and connection options,
// without a connection and without reading a file. The command line calls
// it, so a bad value is an argument error (exit 2) before any request,
// with the same message as the transport gives. The proxy TLS options get
// their --proxy-tls-* names.
func CheckOptions(o *config.Options) error {
	if _, _, err := portRange(o.Net.LocalPort); err != nil {
		return err
	}
	if _, err := newNetRules(o.Net); err != nil {
		return err
	}
	if err := checkTLS(o.Stealth); err != nil {
		return err
	}
	if err := checkTLS(o.ProxyCfg.TLS); err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), "--tls-", "--proxy-tls-"))
	}
	return nil
}

// checkTLS checks the TLS values of one stack, as the transport reads them.
func checkTLS(st config.StealthOptions) error {
	if _, err := cipherSuites(st.Ciphers); err != nil {
		return err
	}
	if _, err := parsePins(st.Pins); err != nil {
		return err
	}
	if err := setTLSVersions(&tls.Config{}, st.TLSMin, st.TLSMax); err != nil {
		return err
	}
	if st.ClientCert == "" {
		return nil
	}
	if st.ClientKey == "" {
		return fmt.Errorf("--tls-cert needs --tls-key")
	}
	switch strings.ToUpper(st.CertType) {
	case "", "PEM", "DER":
		return nil
	}
	return fmt.Errorf("--tls-cert-type %s: use PEM or DER", st.CertType)
}
