package cookies

import (
	"os"
	"path/filepath"
	"testing"
)

// Domain=host makes a domain cookie, not a host-only one (RFC 6265, 5.3
// step 6): the server asked for its subdomains to get it too.
func TestJar_DomainEqualToTheHostCoversSubdomains(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://example.com/", "a=1; Domain=example.com")
	if got := j.HeaderValue("https://www.example.com/"); got != "a=1" {
		t.Errorf("www.example.com gets %q, want a=1", got)
	}
}

// A Secure cookie from an http:// answer is refused: a network attacker
// could otherwise plant a Secure cookie that ghola then sends over
// https (RFC 6265bis, 5.6 step 8; curl refuses it too).
func TestJar_RefusesASecureCookieFromHTTP(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "http://example.com/", "s=planted; Secure")
	if got := j.HeaderValue("https://example.com/"); got != "" {
		t.Errorf("https://example.com gets %q, set Secure over http", got)
	}
	setCookie(t, j, "https://example.com/", "s=ok; Secure")
	if got := j.HeaderValue("https://example.com/"); got != "s=ok" {
		t.Errorf("a Secure cookie over https: %q, want s=ok", got)
	}
}

// --cookie with a value without "=" reads cookies from that file, as curl
// -b FILE does. v1.0.0 dropped the value without a word.
func TestJar_CookieOptionReadsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "in.txt")
	text := "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tFALSE\t0\tfrom\tfile\nother.test\tFALSE\t/\tFALSE\t0\tno\tsend\n"
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	j, _ := New("")
	if err := j.AddRequestCookies("https://www.example.com/", []string{f, "x=1"}); err != nil {
		t.Fatal(err)
	}
	if got := j.HeaderValue("https://www.example.com/"); got != "from=file; x=1" {
		t.Errorf("Cookie = %q, want from=file; x=1", got)
	}
}

// A --cookie file that does not exist adds no cookie and is no error, as
// in curl, where -b with a new name only turns the cookie engine on.
func TestJar_CookieOptionWithAMissingFile(t *testing.T) {
	j, _ := New("")
	if err := j.AddRequestCookies("https://example.com/", []string{filepath.Join(t.TempDir(), "none.txt")}); err != nil {
		t.Errorf("a missing file: %v", err)
	}
}

// A host that is itself a public suffix may set Domain to itself, and the
// cookie stays host-only (RFC 6265, 5.3 step 5): it must not reach the
// other sites under that suffix.
func TestJar_PublicSuffixHostSetsAHostOnlyCookie(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://github.io/", "a=1; Domain=github.io")
	if got := j.HeaderValue("https://github.io/"); got != "a=1" {
		t.Errorf("github.io gets %q, want a=1", got)
	}
	if got := j.HeaderValue("https://other.github.io/"); got != "" {
		t.Errorf("other.github.io gets %q, want nothing", got)
	}
}
