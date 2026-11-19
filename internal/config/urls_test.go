package config

import (
	"slices"
	"strings"
	"testing"
)

// TestParseFlags_URLWithoutScheme checks that a URL without a scheme uses
// http://, as in curl (audit finding M9). Before, "127.0.0.1:8080/x" failed:
// Go reads "127.0.0.1" as an unknown scheme.
func TestParseFlags_URLWithoutScheme(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8080/x":    "http://127.0.0.1:8080/x",
		"localhost:8080":      "http://localhost:8080",
		"example.com/a?b=c":   "http://example.com/a?b=c",
		"https://example.com": "https://example.com",
		"HTTP://example.com":  "HTTP://example.com",
	} {
		opts, _, err := ParseFlags([]string{in})
		if err != nil {
			t.Fatalf("ParseFlags(%q): %v", in, err)
		}
		if opts.URL != want {
			t.Errorf("ParseFlags(%q).URL = %q, want %q", in, opts.URL, want)
		}
	}
}

// TestParseFlags_SeveralURLs checks that every URL on the command line is
// kept, in order (audit finding M9). Before, all but the first were dropped
// without a message.
func TestParseFlags_SeveralURLs(t *testing.T) {
	opts, _, err := ParseFlags([]string{"http://a.example/1", "b.example/2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://a.example/1", "http://b.example/2"}
	if !slices.Equal(opts.URLs, want) || opts.URL != want[0] {
		t.Errorf("URLs = %q, URL = %q; want %q and %q", opts.URLs, opts.URL, want, want[0])
	}
}

// TestParseFlags_SeveralURLsWithOneOutput checks that options that name one
// output refuse a second URL. Two bodies cannot share one file name, and
// the v1 flag design decides how -o pairs with URLs.
func TestParseFlags_SeveralURLsWithOneOutput(t *testing.T) {
	for _, flag := range [][]string{{"-o", "f"}, {"-O"}, {"-w"}, {"-J", "-O"}, {"--har", "h.har"}} {
		args := append(append([]string{}, flag...), "http://a.example/1", "http://b.example/2")
		_, _, err := ParseFlags(args)
		if err == nil || !strings.Contains(err.Error(), "one URL") {
			t.Errorf("ParseFlags(%q) error = %v, want a message about one URL", args, err)
		}
	}
}
