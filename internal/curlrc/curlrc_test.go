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
		got, err := args(c.text)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, err %v; want %q", c.name, got, err, c.want)
		}
	}
}

// An open quote is an error that names the line.
func TestArgs_OpenQuote(t *testing.T) {
	if _, err := Lines("url = \"http://h/\nuser = x\n"); err == nil || err.Error() != "config line 1: a quote is not closed" {
		t.Fatalf("err = %v", err)
	}
}

// args flattens the lines as a command line, for the table above.
func args(text string) ([]string, error) {
	lines, err := Lines(text)
	var out []string
	for _, l := range lines {
		out = append(out, l.Opt)
		if l.HasValue {
			out = append(out, l.Value)
		}
	}
	return out, err
}

// Each line keeps its number, so an error can name the line.
func TestLines_KeepLineNumbers(t *testing.T) {
	lines, err := Lines("# c\n\nsilent yes\n")
	if err != nil || len(lines) != 1 || lines[0] != (Line{N: 3, Opt: "--silent", Value: "yes", HasValue: true}) {
		t.Fatalf("%+v %v", lines, err)
	}
}
