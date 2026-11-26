package cli

import (
	"errors"
	"strings"
	"testing"
)

// A bad value of a TLS or connection option is an argument error (exit
// 2) before any connection, with the message of the check. Before, the
// transport found it at the first request and the run exited 55.
func TestApply_BadTransportValueIsUsageError(t *testing.T) {
	for args, want := range map[string]string{
		"--tls-pin sha256//xyz":                                "--tls-pin",
		"--tls-ciphers NOPE":                                   "--tls-ciphers",
		"--tls-min 1.3 --tls-max 1.2":                          "is above --tls-max",
		"--tls-cert c.pem":                                     "--tls-cert needs --tls-key",
		"--tls-cert c.pem --tls-key k.pem --tls-cert-type P12": "--tls-cert-type P12",
		"--resolve a.test:80":                                  "--resolve",
		"--connect-to a.test:80":                               "--connect-to",
		"--local-port 70000":                                   "--local-port",
		"--proxy-tls-pin sha256//xyz":                          "--proxy-tls-pin",
	} {
		cmd, err := Parse(append(strings.Fields(args), "http://h/"))
		if err == nil {
			_, err = Apply(cmd)
		}
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want a usage error with %q", args, err, want)
		}
	}
}

// URL globs that give too many URLs are an argument error (exit 2), also
// when each URL alone stays below the limit.
func TestApply_TooManyGlobURLsIsUsageError(t *testing.T) {
	for _, args := range [][]string{{"http://h/[1-100000000]"}, {"http://h/a[1-60000]", "http://h/b[1-60000]"}} {
		cmd, err := Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Apply(cmd)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "100000") {
			t.Errorf("%q: err = %v", args, err)
		}
	}
}
