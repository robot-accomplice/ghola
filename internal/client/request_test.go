package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// recordingDoer answers each request with the next handler and keeps a
// copy of every request, so a test can read what ghola sent on each hop.
type recordingDoer struct {
	sent    []*fasthttp.Request
	answers []func(*fasthttp.Response)
}

func (d *recordingDoer) do(_ context.Context, _ *config.Options, req *fasthttp.Request, rsp *fasthttp.Response) error {
	cp := &fasthttp.Request{}
	req.CopyTo(cp)
	d.sent = append(d.sent, cp)
	d.answers[min(len(d.sent)-1, len(d.answers)-1)](rsp)
	return nil
}

func redirectTo(loc string) func(*fasthttp.Response) {
	return func(rsp *fasthttp.Response) {
		rsp.SetStatusCode(fasthttp.StatusFound)
		rsp.Header.Set("Location", loc)
	}
}

func ok(rsp *fasthttp.Response) { rsp.SetStatusCode(fasthttp.StatusOK) }

func fetchAll(t *testing.T, opts *config.Options, d *recordingDoer) {
	t.Helper()
	req, rsp, err := FetchURL(bg(), opts, d.do)
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	Release(req, rsp)
}

// --referer URL must reach the server without --impersonate-headers: it
// is a plain request header, as curl -e sends it. Before, only the profile
// headers carried it, so the plain request had no Referer.
func TestFetchSendsTheRefererURLWithoutProfileHeaders(t *testing.T) {
	d := &recordingDoer{answers: []func(*fasthttp.Response){ok}}
	opts := &config.Options{URL: "http://a.test/x", Method: "GET",
		Stealth: config.StealthOptions{Referer: "https://from.test/page"}}
	fetchAll(t, opts, d)
	if got := string(d.sent[0].Header.Peek("Referer")); got != "https://from.test/page" {
		t.Errorf("Referer = %q, want the --referer URL", got)
	}
}

// --referer auto sends the previous URL after a redirect, as curl -e ";auto"
// does. The first request has no previous URL, so it has no Referer. A
// made-up search-engine Referer would lie to the server.
func TestFetchRefererAutoSendsThePreviousURLAfterARedirect(t *testing.T) {
	for _, stealth := range []bool{false, true} {
		d := &recordingDoer{answers: []func(*fasthttp.Response){redirectTo("http://b.test/next"), ok}}
		opts := &config.Options{URL: "http://a.test/first", Method: "GET",
			Stealth:    config.StealthOptions{Referer: "auto", StealthHeaders: stealth},
			Resilience: config.ResilienceOptions{Location: true, MaxRedirs: 5}}
		fetchAll(t, opts, d)
		if got := string(d.sent[0].Header.Peek("Referer")); got != "" {
			t.Errorf("stealth=%v: first request Referer = %q, want none", stealth, got)
		}
		if got := string(d.sent[1].Header.Peek("Referer")); got != "http://a.test/first" {
			t.Errorf("stealth=%v: second request Referer = %q, want the first URL", stealth, got)
		}
	}
}

// --range and --continue-at must ask the server for the part also when the
// body goes to stdout. Before, only the download path set Range, so stdout
// got the whole body.
func TestFetchSendsRangeOnStdout(t *testing.T) {
	cases := []struct {
		name string
		out  config.OutputOptions
		want string
	}{
		{"range", config.OutputOptions{Range: "0-99"}, "bytes=0-99"},
		{"range with unit", config.OutputOptions{Range: "bytes=5-"}, "bytes=5-"},
		{"continue-at", config.OutputOptions{ContinueAt: "100"}, "bytes=100-"},
		{"continue-at zero", config.OutputOptions{ContinueAt: "0"}, ""},
		{"continue-at dash", config.OutputOptions{ContinueAt: "-"}, ""},
	}
	for _, tc := range cases {
		d := &recordingDoer{answers: []func(*fasthttp.Response){ok}}
		opts := &config.Options{URL: "http://a.test/x", Method: "GET", Output: tc.out}
		fetchAll(t, opts, d)
		if got := string(d.sent[0].Header.Peek("Range")); got != tc.want {
			t.Errorf("%s: Range = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The SigV4 signature covers the method. ghola signed before it set the
// method, so every POST, PUT and DELETE carried a GET signature, and AWS
// refused it. The signature on the wire must match a request signed with
// its own method.
func TestFetchSignsSigV4WithTheRequestMethod(t *testing.T) {
	s, err := config.ParseSigV4("aws:amz:us-east-1:service", "")
	if err != nil {
		t.Fatal(err)
	}
	d := &recordingDoer{answers: []func(*fasthttp.Response){ok}}
	opts := &config.Options{URL: "https://example.amazonaws.com/", Method: "POST", Data: "x=1",
		User: "AK:SK", SigV4: s}
	fetchAll(t, opts, d)
	sent := d.sent[0]
	date := string(sent.Header.Peek("x-amz-date"))
	when, err := time.Parse("20060102T150405Z", date)
	if err != nil {
		t.Fatalf("x-amz-date %q: %v", date, err)
	}
	want := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(want)
	want.SetRequestURI(opts.URL)
	want.Header.SetMethod("POST")
	want.SetBodyString("x=1")
	if err := signSigV4(*s, want, "AK:SK", when); err != nil {
		t.Fatal(err)
	}
	if got, exp := string(sent.Header.Peek("Authorization")), string(want.Header.Peek("Authorization")); got != exp {
		t.Errorf("Authorization\n got %s\nwant %s (the POST signature)", got, exp)
	}
}

// AWS signs the path of a service other than S3 encoded twice, and the S3
// path once. An encoded slash (%2F) is part of a name and must stay %2F on
// the wire; it is not a path separator.
func TestSigV4CanonicalPathEncodesTwiceExceptS3(t *testing.T) {
	if got := canonicalPath("/a%2Fb/c%20d", "service"); got != "/a%252Fb/c%2520d" {
		t.Errorf("non-S3 path = %q, want each segment encoded twice", got)
	}
	if got := canonicalPath("/a%2Fb/c%20d", "s3"); got != "/a%2Fb/c%20d" {
		t.Errorf("S3 path = %q, want the path as sent", got)
	}
	if got := canonicalPath("", "service"); got != "/" {
		t.Errorf("empty path = %q, want /", got)
	}
}

func TestFetchKeepsAnEncodedSlashInThePath(t *testing.T) {
	d := &recordingDoer{answers: []func(*fasthttp.Response){ok}}
	opts := &config.Options{URL: "http://a.test/a%2Fb", Method: "GET"}
	fetchAll(t, opts, d)
	if got := d.sent[0].URI().String(); !strings.Contains(got, "%2F") {
		t.Errorf("request target = %q, want %%2F kept", got)
	}
}

// A Retry-After wait that is longer than --retry-max-time allows must not
// run: the retry could never start in time. ghola waited for the whole
// Retry-After value (99999 s: 27 hours). The 503 answer is the result.
func TestFetchDoesNotWaitARetryAfterBeyondTheRetryTime(t *testing.T) {
	d := &recordingDoer{answers: []func(*fasthttp.Response){func(rsp *fasthttp.Response) {
		rsp.SetStatusCode(503)
		rsp.Header.Set("Retry-After", "99999")
	}}}
	opts := &config.Options{URL: "http://a.test/x", Method: "GET",
		Output:     config.OutputOptions{Silent: true},
		Resilience: config.ResilienceOptions{Retries: 3, RetryHTTP: true, RetryMaxTime: 3 * time.Second}}
	start := time.Now()
	req, rsp, err := FetchURL(bg(), opts, d.do)
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	defer Release(req, rsp)
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v: the Retry-After wait ran past --retry-max-time", took)
	}
	if rsp.StatusCode() != 503 || len(d.sent) != 1 {
		t.Errorf("status %d after %d requests; want the 503 after 1 request", rsp.StatusCode(), len(d.sent))
	}
}

// The --timeout deadline also caps the wait: a wait that ends after the
// deadline cannot be followed by a request.
func TestFetchDoesNotWaitARetryAfterBeyondTheDeadline(t *testing.T) {
	d := &recordingDoer{answers: []func(*fasthttp.Response){func(rsp *fasthttp.Response) {
		rsp.SetStatusCode(503)
		rsp.Header.Set("Retry-After", "60")
	}}}
	opts := &config.Options{URL: "http://a.test/x", Method: "GET",
		Output:     config.OutputOptions{Silent: true},
		Resilience: config.ResilienceOptions{Retries: 3, RetryHTTP: true}}
	ctx, cancel := context.WithTimeout(bg(), 5*time.Second)
	defer cancel()
	start := time.Now()
	req, rsp, err := FetchURL(ctx, opts, d.do)
	if err != nil {
		t.Fatalf("FetchURL: %v", err)
	}
	defer Release(req, rsp)
	if took := time.Since(start); took > time.Second || rsp.StatusCode() != 503 {
		t.Errorf("took %v, status %d; want the 503 at once", took, rsp.StatusCode())
	}
}
