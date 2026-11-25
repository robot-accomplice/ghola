package config

import (
	"strings"
	"testing"
)

func finish(t *testing.T, o *Options, explicit ...string) error {
	t.Helper()
	if o.Method == "" {
		o.Method = "GET"
	}
	if o.Concurrency == 0 {
		o.Concurrency = 1
	}
	if o.Stealth.Referer == "" {
		o.Stealth.Referer = "none"
	}
	set := map[string]bool{}
	for _, e := range explicit {
		set[e] = true
	}
	return Finish(o, func(name string) bool { return set[name] })
}

func withURL(o *Options, urls ...string) *Options {
	TakeURLs(o, urls)
	return o
}

// A URL without a scheme gets http://, as in curl (audit finding M9).
func TestFinish_URLWithoutScheme(t *testing.T) {
	o := withURL(&Options{}, "example.com:8080/x", "https://h/y")
	if err := finish(t, o); err != nil {
		t.Fatal(err)
	}
	if o.URL != "http://example.com:8080/x" || o.URLs[1] != "https://h/y" {
		t.Errorf("URLs = %q", o.URLs)
	}
}

// A transfer needs a URL.
func TestFinish_URLIsMandatory(t *testing.T) {
	if err := finish(t, &Options{}); err == nil || !strings.Contains(err.Error(), "URL is mandatory") {
		t.Fatalf("err = %v", err)
	}
}

// A body makes the request a POST, unless the method was given.
func TestFinish_PostForABody(t *testing.T) {
	o := withURL(&Options{Data: "a=1"}, "h")
	finish(t, o) //nolint:errcheck
	if o.Method != "POST" {
		t.Errorf("method = %s", o.Method)
	}
	o = withURL(&Options{Data: "a=1", Method: "PUT"}, "h")
	finish(t, o, ExplicitMethod) //nolint:errcheck
	if o.Method != "PUT" {
		t.Errorf("explicit method = %s", o.Method)
	}
}

// A form body gets the form Content-Type, as in curl, unless -H sets one
// (audit finding M1). A GET gets none.
func TestFinish_ContentTypeAsCurl(t *testing.T) {
	o := withURL(&Options{Data: "a=1"}, "h")
	finish(t, o) //nolint:errcheck
	if !hasHeader(o.Headers, "Content-Type") || o.Headers[0] != formContentType {
		t.Errorf("headers = %q", o.Headers)
	}
	o = withURL(&Options{Data: "{}", Headers: []string{"Content-Type: application/json"}}, "h")
	finish(t, o) //nolint:errcheck
	if len(o.Headers) != 1 {
		t.Errorf("a second Content-Type was added: %q", o.Headers)
	}
	o = withURL(&Options{}, "h")
	finish(t, o) //nolint:errcheck
	if hasHeader(o.Headers, "Content-Type") {
		t.Errorf("a GET got a Content-Type: %q", o.Headers)
	}
}

// The chain shortcut sets the JSON-RPC headers; an unknown chain is an
// error.
func TestFinish_Chain(t *testing.T) {
	o := withURL(&Options{Chain: "eth"}, "h")
	if err := finish(t, o); err != nil || !hasHeader(o.Headers, "Content-Type") {
		t.Fatalf("err %v, headers %q", err, o.Headers)
	}
}

// Options that cannot go together are refused before any request.
func TestFinish_Conflicts(t *testing.T) {
	cases := map[string]*Options{
		"continue and range":    {Output: OutputOptions{ContinueAt: "-", Range: "0-9"}},
		"compressed and resume": {Output: OutputOptions{ContinueAt: "-"}, Stealth: StealthOptions{Compressed: true}},
		"compressed and range":  {Output: OutputOptions{Range: "0-9"}, Stealth: StealthOptions{Compressed: true}},
		"proxy and proxy file":  {ProxyCfg: ProxyOptions{Proxy: "http://p", File: "f"}},
		"form and data":         {Form: []string{"a=1"}, Data: "x"},
		"bad continue offset":   {Output: OutputOptions{ContinueAt: "abc"}},
		"bad referer":           {Stealth: StealthOptions{Referer: "::not a url"}},
		"header without colon":  {Headers: []string{"NoColon"}},
	}
	for name, o := range cases {
		if err := finish(t, withURL(o, "h")); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// "Name:" removes a header and is accepted (audit finding M7).
func TestFinish_HeaderRemovalIsAccepted(t *testing.T) {
	if err := finish(t, withURL(&Options{Headers: []string{"User-Agent:"}}, "h")); err != nil {
		t.Fatal(err)
	}
}

// One -o FILE and one --har file take one URL (audit finding M9), so
// two bodies cannot share one file. The remote name and one -o for each
// URL serve several (v1 spec, output group).
func TestFinish_SeveralURLsWithOneOutput(t *testing.T) {
	for _, o := range []OutputOptions{{File: "f"}, {HAR: "h.har"}} {
		if err := finish(t, withURL(&Options{Output: o}, "a", "b")); err == nil {
			t.Errorf("%+v with two URLs was accepted", o)
		}
	}
	for _, o := range []OutputOptions{{RemoteName: true}, {File: "f", PerURL: []string{"f", "g"}}} {
		if err := finish(t, withURL(&Options{Output: o}, "a", "b")); err != nil {
			t.Errorf("%+v with two URLs: %v", o, err)
		}
	}
}

// --follow-trusted turns redirects on, as curl --location-trusted does.
func TestFinish_TrustedFollowsRedirects(t *testing.T) {
	o := withURL(&Options{Resilience: ResilienceOptions{LocationTrusted: true}}, "h")
	finish(t, o) //nolint:errcheck
	if !o.Resilience.Location {
		t.Error("redirects stay off")
	}
}

// The remote name is the last path part, index.html for a bare host, and
// goes inside -o DIR/.
func TestFinish_RemoteName(t *testing.T) {
	for url, want := range map[string]string{"https://h/a/file.zip": "file.zip", "https://h/": "index.html", "https://h/x/..%5C..%5Cup.txt": "up.txt", "https://h/C:%5Cabs.txt": "abs.txt"} {
		o := withURL(&Options{Output: OutputOptions{RemoteName: true}}, url)
		finish(t, o) //nolint:errcheck
		if o.Output.File != want {
			t.Errorf("%s: name %q, want %q", url, o.Output.File, want)
		}
	}
}

func TestExitCode_Int(t *testing.T) {
	if HTTPError.Int() != 22 {
		t.Errorf("HTTPError = %d", HTTPError.Int())
	}
}
