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

// The same rule on the stdout path: -L to another host drops -u.
func TestFetchURL_UserCredentialsDoNotFollowCrossHostRedirect(t *testing.T) {
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

	opts := &config.Options{
		URL:         "http://test/start",
		Method:      "GET",
		User:        "alice:s3cret",
		Stealth:     config.StealthOptions{Agent: "ghola"},
		Resilience:  config.ResilienceOptions{Location: true, MaxRedirs: 5, Backoff: 1},
		Output:      config.OutputOptions{Silent: true},
		Concurrency: 1,
	}
	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil {
		t.Fatalf("FetchURL error: %v", err)
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if lastHost != "other" {
		t.Fatalf("last request went to %q, want other", lastHost)
	}
	if lastAuth != "" {
		t.Fatalf("redirect to another host carried Authorization %q", lastAuth)
	}
}
