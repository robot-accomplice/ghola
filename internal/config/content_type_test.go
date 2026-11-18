package config

import (
	"strings"
	"testing"
)

func contentTypes(headers []string) []string {
	var out []string
	for _, h := range headers {
		if k, v, ok := strings.Cut(h, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Type") {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// Content-Type follows curl (audit finding M1). Before, every request got
// Content-Type: application/json: a GET too, which breaks the browser
// fingerprint of --impersonate, and form data was labelled as JSON.
func TestParseFlags_ContentTypeAsCurl(t *testing.T) {
	const form = "application/x-www-form-urlencoded"
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"GET has none", nil, nil},
		{"-H other keeps none", []string{"-H", "X-A: 1"}, nil},
		{"-d is a form", []string{"-d", "a=1"}, []string{form}},
		{"--data-binary is a form", []string{"--data-binary", "x"}, []string{form}},
		{"--data-urlencode is a form", []string{"--data-urlencode", "a=b c"}, []string{form}},
		{"-H wins over the default", []string{"-d", "{}", "-H", "content-type: application/json"}, []string{"application/json"}},
		{"--chain is JSON-RPC", []string{"-c", "eth", "-d", "{}"}, []string{"application/json"}},
	} {
		opts, _, err := ParseFlags(append(tc.args, "http://example.com"))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := contentTypes(opts.Headers)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: Content-Type = %q, want %q", tc.name, got, tc.want)
		}
	}
}
