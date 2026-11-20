package cookies

import (
	"testing"

	"github.com/valyala/fasthttp"
)

func setCookie(t *testing.T, j *Jar, from string, values ...string) {
	t.Helper()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	for _, v := range values {
		rsp.Header.Add("Set-Cookie", v)
	}
	j.AbsorbResponse(from, rsp)
}

// A server may set a cookie only for its own domain (RFC 6265, 5.3 step 6).
// Before, any Domain was stored, so evil.example could plant a session
// cookie that ghola later sent to bank.example.
func TestJar_RefusesACookieForAnotherDomain(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://evil.example/", "session=planted; Domain=bank.example")
	if got := j.HeaderValue("https://bank.example/"); got != "" {
		t.Errorf("bank.example gets %q, set by evil.example", got)
	}
}

// A cookie for a public suffix (Domain=com) is refused (RFC 6265, 5.3 step 5).
func TestJar_RefusesAPublicSuffixDomain(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://shop.example.com/", "track=1; Domain=com")
	if got := j.HeaderValue("https://other.com/"); got != "" {
		t.Errorf("other.com gets %q", got)
	}
}

// A cookie without Domain is host-only: it goes back to that host and not
// to its subdomains (RFC 6265, 5.3 step 6).
func TestJar_HostOnlyCookieStaysOnItsHost(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://example.com/", "a=1")
	if got := j.HeaderValue("https://example.com/"); got != "a=1" {
		t.Errorf("example.com gets %q, want a=1", got)
	}
	if got := j.HeaderValue("https://sub.example.com/"); got != "" {
		t.Errorf("sub.example.com gets the host-only cookie: %q", got)
	}
}

// A Domain cookie goes to the subdomains too.
func TestJar_DomainCookieReachesSubdomains(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://www.example.com/", "a=1; Domain=example.com")
	if got := j.HeaderValue("https://api.example.com/"); got != "a=1" {
		t.Errorf("api.example.com gets %q, want a=1", got)
	}
}

// Path /foo matches /foo and /foo/bar, not /foobar (RFC 6265, 5.1.4).
func TestJar_PathMatchStopsAtASegment(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://example.com/foo/x", "a=1; Path=/foo")
	if got := j.HeaderValue("https://example.com/foo/bar"); got != "a=1" {
		t.Errorf("/foo/bar gets %q, want a=1", got)
	}
	if got := j.HeaderValue("https://example.com/foobar"); got != "" {
		t.Errorf("/foobar gets %q", got)
	}
}

// Max-Age=0 deletes the cookie (RFC 6265, 5.2.2).
func TestJar_MaxAgeZeroDeletes(t *testing.T) {
	j, _ := New("")
	setCookie(t, j, "https://example.com/", "a=1")
	setCookie(t, j, "https://example.com/", "a=1; Max-Age=0")
	if got := j.HeaderValue("https://example.com/"); got != "" {
		t.Errorf("the deleted cookie is still sent: %q", got)
	}
}
