package curlrc

import (
	"reflect"
	"testing"
)

// Each case is a config file and the arguments that it means. The value
// curl sent for the "user" line is what curl 8.18.0 did with the same file
// on 2026-11-20 (curl -K file, to a server that echoes the credentials).
func TestArgs_MatchesCurl(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"quoted with escapes", "url = \"http://h/\"\nuser = \"a\\\\b\\tc:d\\\"e\"\n",
			[]string{"--url", "http://h/", "--user", "a\\b\tc:d\"e"}},
		{"colon separators", "url: http://h/\nuser:plain:pw\n",
			[]string{"--url", "http://h/", "--user", "plain:pw"}},
		{"dashed forms", "--url http://h/\n-u x:y\n",
			[]string{"--url", "http://h/", "-u", "x:y"}},
		{"backslash before another letter", "   # indented comment\nuser = \"q\\zw:1\"\n",
			[]string{"--user", "qzw:1"}},
		{"unquoted value ends at whitespace", "user = u1:p1 trailing\n",
			[]string{"--user", "u1:p1"}},
		{"no spaces around =", "user=n:\"x\"\n",
			[]string{"--user", "n:\"x\""}},
		{"a flag without a value", "silent\n-O\n",
			[]string{"--silent", "-O"}},
	}
	for _, c := range cases {
		got, err := Args(c.text)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, err %v; want %q", c.name, got, err, c.want)
		}
	}
}

// An open quote is an error that names the line.
func TestArgs_OpenQuote(t *testing.T) {
	if _, err := Args("url = \"http://h/\nuser = x\n"); err == nil || err.Error() != "config line 1: a quote is not closed" {
		t.Fatalf("err = %v", err)
	}
}
