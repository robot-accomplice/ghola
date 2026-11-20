package glob

import (
	"reflect"
	"strings"
	"testing"
)

func expand(t *testing.T, url string) []string {
	t.Helper()
	p, err := Parse(url)
	if err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	var out []string
	p.Each(func(u string, _ []string) bool { out = append(out, u); return true })
	return out
}

// Each expected list is what curl 8.18.0 gave on 2026-11-20 for the same
// URL (curl -s -w '%{url}\n' -o /dev/null URL).
func TestExpand_MatchesCurl(t *testing.T) {
	cases := map[string][]string{
		"file:///nonexistent/{a,b}x[1-3:2]y[08-10]": {
			"file:///nonexistent/ax1y08", "file:///nonexistent/ax1y09", "file:///nonexistent/ax1y10",
			"file:///nonexistent/ax3y08", "file:///nonexistent/ax3y09", "file:///nonexistent/ax3y10",
			"file:///nonexistent/bx1y08", "file:///nonexistent/bx1y09", "file:///nonexistent/bx1y10",
			"file:///nonexistent/bx3y08", "file:///nonexistent/bx3y09", "file:///nonexistent/bx3y10",
		},
		"file:///n/[a-e:2]":      {"file:///n/a", "file:///n/c", "file:///n/e"},
		`file:///n/\{x,y\}[1-2]`: {"file:///n/{x,y}1", "file:///n/{x,y}2"},
		"file:///n/{a,}b":        {"file:///n/ab", "file:///n/b"},
		"file:///n/[1-2]":        {"file:///n/1", "file:///n/2"},
	}
	for url, want := range cases {
		if got := expand(t, url); !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", url, got, want)
		}
	}
}

// Each error message and position is what curl 8.18.0 printed (exit 3).
func TestParse_ErrorsMatchCurl(t *testing.T) {
	cases := map[string]string{
		"file:///n/{a,b":     "unmatched brace in URL position 11",
		"file:///n/[1-":      "bad range in URL position 14",
		"file:///n/[3-1]":    "bad range in URL position 16",
		"file:///n/[a-3]":    "bad range in URL position 16",
		"file:///n/{a{b,c}}": "nested brace in URL position 13",
		"file:///n/[1-2:0]":  "bad range in URL position 18",
		"file:///n/]x":       "unmatched close brace/bracket in URL position 11",
	}
	for url, want := range cases {
		_, err := Parse(url)
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", url, err, want)
		}
	}
}

// An IPv6 host in brackets is not a range.
func TestParse_IPv6HostIsLiteral(t *testing.T) {
	for _, url := range []string{"http://[::1]:8080/x", "http://[fe80::1%25eth0]/"} {
		if got := expand(t, url); len(got) != 1 || got[0] != url {
			t.Errorf("%s gives %q", url, got)
		}
	}
	if got := expand(t, "http://[::1]/f[1-2]"); !reflect.DeepEqual(got, []string{"http://[::1]/f1", "http://[::1]/f2"}) {
		t.Errorf("got %q", got)
	}
}

// #N in an -o name takes the value of the Nth glob, as in curl's manual:
// curl "http://{site,host}.host[1-5].example" -o "#1_#2".
func TestOutputName(t *testing.T) {
	p, _ := Parse("http://{site,host}.host[1-5].example")
	var names []string
	p.Each(func(_ string, v []string) bool {
		names = append(names, OutputName("#1_#2", v))
		return len(names) < 3
	})
	if strings.Join(names, " ") != "site_1 site_2 site_3" {
		t.Errorf("names = %q", names)
	}
	if got := OutputName("a#b#9.txt", []string{"x"}); got != "a#b#9.txt" {
		t.Errorf("a # that names no glob changed: %q", got)
	}
}

func TestCount(t *testing.T) {
	p, _ := Parse("http://x/{a,b,c}[1-10]")
	if p.Count() != 30 {
		t.Errorf("Count = %d", p.Count())
	}
}
