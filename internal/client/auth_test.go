package client

import (
	"encoding/base64"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

var aliceAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))

// -u must reach the server on a download to a file, as it does on stdout.
// Before the fix only FetchURL added it, so `ghola -u a:b -o f URL` got 401
// (audit finding H8).
func TestBuildDownloadRequest_SendsUserCredentials(t *testing.T) {
	opts := &config.Options{URL: "http://test/f", User: "alice:s3cret"}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	buildDownloadRequest(req, opts, opts.URL, fasthttp.MethodGet, "")

	if got := string(req.Header.Peek("Authorization")); got != aliceAuth {
		t.Fatalf("Authorization = %q, want %q", got, aliceAuth)
	}
}

// -u credentials are for the server the user named. A redirect to another
// scheme, host or port must not receive them (curl behaves the same way).
// Otherwise any server that redirects can collect the password (audit
// finding H1).
func TestBuildDownloadRequest_UserCredentialsOnlyForTheOriginalServer(t *testing.T) {
	opts := &config.Options{URL: "http://Test:80/start", User: "alice:s3cret"}
	for _, tc := range []struct {
		target string
		sent   bool
	}{
		{"http://test/other/path", true}, // same server, other path
		{"http://test:80/f", true},       // explicit default port
		{"http://evil/f", false},         // other host
		{"http://test:8080/f", false},    // other port
		{"https://test/f", false},        // other scheme
	} {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, tc.target, fasthttp.MethodGet, "")
		got := string(req.Header.Peek("Authorization"))
		fasthttp.ReleaseRequest(req)
		if tc.sent && got != aliceAuth {
			t.Errorf("%s: Authorization = %q, want %q", tc.target, got, aliceAuth)
		}
		if !tc.sent && got != "" {
			t.Errorf("%s: Authorization = %q, want none", tc.target, got)
		}
	}
}

// authAfterCrossHostRedirect runs FetchURL against a server that redirects
// from host test to host other. It returns the Authorization header that
// reached host other.
func authAfterCrossHostRedirect(t *testing.T, opts *config.Options) string {
	t.Helper()
	var lastHost, lastAuth string
	ln, do := newTestServer(func(ctx *fasthttp.RequestCtx) {
		lastHost = string(ctx.Host())
		lastAuth = string(ctx.Request.Header.Peek("Authorization"))
		if lastHost == "test" {
			ctx.SetStatusCode(302)
			ctx.Response.Header.Set("Location", "http://other/echo")
			return
		}
		ctx.SetStatusCode(200)
	})
	defer ln.Close()

	opts.URL = "http://test/start"
	opts.Method = "GET"
	opts.Stealth = config.StealthOptions{Agent: "ghola"}
	opts.Resilience = config.ResilienceOptions{Location: true, MaxRedirs: 5, Backoff: 1}
	opts.Output = config.OutputOptions{Silent: true}
	opts.Concurrency = 1
	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if lastHost != "other" {
		t.Fatalf("last request went to %q, want other", lastHost)
	}
	return lastAuth
}

// The same rule on the stdout path: -L to another host drops -u.
func TestFetchURL_UserCredentialsDoNotFollowCrossHostRedirect(t *testing.T) {
	if got := authAfterCrossHostRedirect(t, &config.Options{User: "alice:s3cret"}); got != "" {
		t.Fatalf("redirect to another host carried Authorization %q", got)
	}
}

// A custom Authorization or Cookie header from -H is a credential too. curl
// drops both on a redirect to another scheme, host or port, and so must
// ghola. Before the fix -H "Authorization: Bearer tok" went to every server
// in the redirect chain (audit finding H1, parity scenario xhost_bearer).
func TestBuildDownloadRequest_CustomCredentialHeadersOnlyForTheOriginalServer(t *testing.T) {
	opts := &config.Options{
		URL:     "http://test/start",
		Headers: []string{"authorization: Bearer tok", "Cookie: sid=abc", "X-Other: kept"},
	}
	for _, tc := range []struct {
		target string
		sent   bool
	}{
		{"http://test/f", true},
		{"http://evil/f", false},
		{"http://test:8080/f", false},
		{"https://test/f", false},
	} {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, tc.target, fasthttp.MethodGet, "")
		auth := string(req.Header.Peek("Authorization"))
		cookie := string(req.Header.Peek("Cookie"))
		other := string(req.Header.Peek("X-Other"))
		fasthttp.ReleaseRequest(req)
		if tc.sent && (auth != "Bearer tok" || cookie != "sid=abc") {
			t.Errorf("%s: Authorization = %q, Cookie = %q, want both sent", tc.target, auth, cookie)
		}
		if !tc.sent && (auth != "" || cookie != "") {
			t.Errorf("%s: Authorization = %q, Cookie = %q, want neither", tc.target, auth, cookie)
		}
		if other != "kept" {
			t.Errorf("%s: X-Other = %q, want kept: only credential headers are dropped", tc.target, other)
		}
	}
}

// --location-trusted is the user's explicit choice to send the credentials
// to every server in the redirect chain, as curl's flag of the same name.
func TestBuildDownloadRequest_LocationTrustedSendsCredentialsToEveryServer(t *testing.T) {
	opts := &config.Options{
		URL:        "http://test/start",
		User:       "alice:s3cret",
		Headers:    []string{"Cookie: sid=abc"},
		Resilience: config.ResilienceOptions{LocationTrusted: true},
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, "http://evil/f", fasthttp.MethodGet, "")
	if got := string(req.Header.Peek("Authorization")); got != aliceAuth {
		t.Errorf("Authorization = %q, want %q", got, aliceAuth)
	}
	if got := string(req.Header.Peek("Cookie")); got != "sid=abc" {
		t.Errorf("Cookie = %q, want sid=abc", got)
	}
}

// The stdout path follows the same rule: -L to another host drops a custom
// Authorization header.
func TestFetchURL_CustomAuthorizationDoesNotFollowCrossHostRedirect(t *testing.T) {
	opts := &config.Options{Headers: []string{"Authorization: Bearer tok"}}
	if got := authAfterCrossHostRedirect(t, opts); got != "" {
		t.Fatalf("redirect to another host carried Authorization %q", got)
	}
}
