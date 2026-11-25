package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// curlOptions runs ghola curl ARGS through the native parser and Apply,
// so a test sees what the request sends.
func curlOptions(t *testing.T, args ...string) *config.Options {
	t.Helper()
	cmd, err := Parse(fromCurl(t, args...).Args)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// withStdin makes text the process stdin for the test.
func withStdin(t *testing.T, text string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(text) //nolint:errcheck
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// curl sends one body made of every data option: the -d and
// --data-urlencode parts join with &, and a --json part joins with
// nothing. Before, ghola curl kept only the last part, so the server got
// a part of the form and the run still exited 0.
func TestCurl_BodyJoinsEveryDataPart(t *testing.T) {
	for want, args := range map[string][]string{
		"a=1&b=2":   {"-d", "a=1", "-d", "b=2", "u"},
		"a=1&b=c+d": {"-d", "a=1", "--data-urlencode", "b=c d", "u"},
		`{"a":1}`:   {"--json", `{"a":`, "--json", "1}", "u"},
		"a=1&@x":    {"--data-binary", "a=1", "--data-raw", "@x", "u"},
	} {
		if got := curlOptions(t, args...).Data; got != want {
			t.Errorf("%q: body %q, want %q", args, got, want)
		}
	}
}

// --json sets Content-Type and Accept to application/json, unless -H
// sets them.
func TestCurl_JSONSetsTypeUnlessHeaderDoes(t *testing.T) {
	o := curlOptions(t, "--json", "{}", "-H", "Content-Type: text/x", "u")
	if !hasHeader(o.Headers, "Accept") || !strings.Contains(strings.Join(o.Headers, "\n"), "Content-Type: text/x") ||
		strings.Contains(strings.Join(o.Headers, "\n"), "Content-Type: application/json") {
		t.Errorf("headers = %q", o.Headers)
	}
}

// curl -d @file, --data-ascii @file and -d @- drop the CR and LF bytes of
// the file; --data-binary keeps them. Before, ghola curl sent them all.
func TestCurl_DataFileDropsLineBreaks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.txt")
	os.WriteFile(path, []byte("a=1\r\nb=2\n\nc=3\n"), 0o644) //nolint:errcheck
	for _, flag := range []string{"-d", "--data", "--data-ascii"} {
		if got := curlOptions(t, flag, "@"+path, "u").Data; got != "a=1b=2c=3" {
			t.Errorf("%s @file: body %q", flag, got)
		}
	}
	if got := curlOptions(t, "--data-binary", "@"+path, "u").Data; got != "a=1\r\nb=2\n\nc=3\n" {
		t.Errorf("--data-binary @file: body %q", got)
	}
	withStdin(t, "x=1\r\ny=2\n")
	if got := curlOptions(t, "-d", "@-", "u").Data; got != "x=1y=2" {
		t.Errorf("-d @-: body %q", got)
	}
}

// --data-urlencode takes curl's five forms: content, =content,
// name=content, @file and name@file. Before, "=a b" kept the = and
// "f@v.txt" sent the file name.
func TestCurl_DataURLEncodeForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.txt")
	os.WriteFile(path, []byte("x y\n"), 0o644) //nolint:errcheck
	for arg, want := range map[string]string{
		"a b":       "a+b",
		"=a b":      "a+b",
		"q=a b&c":   "q=a+b%26c",
		"f@" + path: "f=x+y%0A",
		"@" + path:  "x+y%0A",
	} {
		if got := curlOptions(t, "--data-urlencode", arg, "u").Data; got != want {
			t.Errorf("--data-urlencode %q: body %q, want %q", arg, got, want)
		}
	}
}

// curl -G sends the data as the query of a GET, not as a body.
func TestCurl_GetPutsDataInQuery(t *testing.T) {
	o := curlOptions(t, "-G", "-d", "a=1", "--data-urlencode", "b=c d", "http://h/p?x=1")
	if o.Data != "" || o.Method != "GET" || o.URL != "http://h/p?x=1&a=1&b=c+d" {
		t.Errorf("data %q, method %s, URL %s", o.Data, o.Method, o.URL)
	}
}

// curl -d "" sends an empty POST body.
func TestCurl_EmptyDataStillPosts(t *testing.T) {
	if o := curlOptions(t, "-d", "", "u"); o.Method != "POST" {
		t.Errorf("method %s", o.Method)
	}
	if o := curlOptions(t, "-X", "PUT", "-d", "", "u"); o.Method != "PUT" {
		t.Errorf("-X PUT: method %s", o.Method)
	}
}
