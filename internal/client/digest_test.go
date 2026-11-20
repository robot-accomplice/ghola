package client

import (
	"strings"
	"testing"
)

// The answer matches the worked example of RFC 2617, section 3.5, which
// was computed by another implementation: a wrong hash order or field
// would not give its response value.
func TestDigestAnswer_RFC2617Example(t *testing.T) {
	ch := parseAuthParams(`realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c403ebaf9f0171e9517f40e41"`)
	got, err := digestAnswer("Mufasa:Circle Of Life", ch, "GET", "/dir/index.html", "0a4f113b")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`response="6629fae49393a05397450978507c4ef1"`, `qop=auth`, `nc=00000001`, `opaque="5ccc069c403ebaf9f0171e9517f40e41"`, `uri="/dir/index.html"`} {
		if !strings.Contains(got, want) {
			t.Errorf("answer %s\nhas no %s", got, want)
		}
	}
}

// An algorithm ghola does not know is an error, not a wrong answer.
func TestDigestAnswer_UnknownAlgorithm(t *testing.T) {
	if _, err := digestAnswer("u:p", map[string]string{"algorithm": "SHA-512-256"}, "GET", "/", "c"); err == nil {
		t.Error("SHA-512-256 was accepted")
	}
}
