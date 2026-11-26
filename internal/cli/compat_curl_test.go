package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A -K file that reads itself stops with an argument error after 5
// levels, as curl does. Before, ghola curl ran forever.
func TestCurl_ConfigNestsAtMostFiveDeep(t *testing.T) {
	path := writeFile(t, "k.txt", "")
	os.WriteFile(path, []byte("-K "+path+"\n"), 0o644) //nolint:errcheck
	done := make(chan error, 1)
	go func() { _, err := FromCurl([]string{"-K", path, "u"}); done <- err }()
	select {
	case err := <-done:
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "5") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a -K file that reads itself does not stop")
	}
	// Five levels still work.
	last := writeFile(t, "k5.txt", "url = http://h/deep\n")
	for i := 0; i < 4; i++ {
		last = writeFile(t, "k.txt", "-K "+last+"\n")
	}
	if c := fromCurl(t, "-K", last); c.Args[len(c.Args)-1] != "http://h/deep" {
		t.Errorf("args = %q", c.Args)
	}
}

// curl -K - reads the config from stdin.
func TestCurl_ConfigFromStdin(t *testing.T) {
	withStdin(t, "url = \"http://h/in\"\n")
	if c := fromCurl(t, "-K", "-"); c.Args[len(c.Args)-1] != "http://h/in" {
		t.Errorf("args = %q", c.Args)
	}
}

// A -K line with a value after an option that takes none is an error, as
// in curl. Before, "silent yes" fetched http://yes/.
func TestCurl_ConfigRefusesValueAfterFlag(t *testing.T) {
	path := writeFile(t, "k.txt", "url = http://h/\nsilent yes\n")
	_, err := FromCurl([]string{"-K", path})
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "--silent") {
		t.Fatalf("err = %v", err)
	}
}

// curl reads --name=value, on the command line and in a -K file.
func TestCurl_LongOptionWithEquals(t *testing.T) {
	path := writeFile(t, "k.txt", "--url=http://h/k\n")
	c := fromCurl(t, "--output=x.txt", "-K", path)
	if got := strings.Join(c.Args[len(curlSpec.defaults):], " "); got != "-o x.txt http://h/k" {
		t.Errorf("args = %q", got)
	}
	if _, err := FromCurl([]string{"--location=yes", "u"}); err == nil {
		t.Error("--location=yes: no error")
	}
}

// Every curl flag that the curl column of v1-options.csv names runs in
// ghola curl, so the spec and the compatibility command cannot drift.
// Before, --output-dir, --no-clobber, -G, -Z, --parallel-max,
// --tls13-ciphers and --stderr were in the spec and refused.
func TestCurl_RunsEveryFlagOfTheSpec(t *testing.T) {
	cfg := writeFile(t, "k.txt", "")
	for _, o := range table {
		for _, f := range flags(o.Curl) {
			args := []string{f}
			if curlSpec.valueTaker(f) {
				args = append(args, map[string]string{"-K": cfg, "--variable": "a=1", "--ech": "true"}[f])
				if len(args) == 1 || args[1] == "" {
					args = append(args[:1], "1")
				}
			}
			if _, err := FromCurl(append(args, "http://h/")); err != nil {
				t.Errorf("--%s: curl %s: %v", o.Name, f, err)
			}
		}
	}
}

// curl pairs each -o and -O with the next URL; a URL without one goes to
// stdout, or under its remote name with --remote-name-all. --output-dir
// holds the files, and -O on a URL without a file name writes
// curl_response.
func TestCurl_OutputsPairWithURLs(t *testing.T) {
	for want, args := range map[string][]string{
		"-o ./ -o - http://h/a http://h/b":                {"-O", "http://h/a", "http://h/b"},
		"-o ./ http://h/a http://h/b":                     {"--remote-name-all", "http://h/a", "http://h/b"},
		"-o x -o ./ http://h/a http://h/b":                {"--remote-name-all", "-o", "x", "http://h/a", "http://h/b"},
		"-o d/ http://h/a":                                {"--output-dir", "d", "-O", "http://h/a"},
		"-o d/x.txt http://h/a":                           {"--output-dir", "d", "-o", "x.txt", "http://h/a"},
		"-o ./ 'http://h/g[1-2]'":                         {"-O", "http://h/g[1-2]"},
		"-o curl_response -o ./ http://h/ http://h/b.txt": {"-O", "-O", "http://h/", "http://h/b.txt"},
		"-o curl_response http://h/":                      {"-O", "http://h/"},
	} {
		got := strings.TrimPrefix(native(t, FromCurl, args...), ShellQuote(curlSpec.defaults)+" ")
		if got != want {
			t.Errorf("%q:\n got %s\nwant %s", args, got, want)
		}
	}
}

// The other curl options that ghola curl refused: --no-NAME of an on/off
// option, -N, -q after the first argument, -Z with --parallel-max, -T
// to a URL that ends in /, and -e ';auto'.
func TestCurl_MoreOptions(t *testing.T) {
	for want, args := range map[string][]string{
		"--no-follow --no-fail --no-compressed --tls-verify u": {"--no-location", "--no-fail", "--no-compressed", "--no-insecure", "u"},
		"u":                                              {"-N", "u", "-q"},
		"-o ./ --jobs 2 http://h/a http://h/b":           {"-Z", "--parallel-max", "2", "--remote-name-all", "http://h/a", "http://h/b"},
		"-o ./ --jobs 50 u":                              {"-Z", "-O", "u"},
		"http://h/a http://h/b":                          {"-Z", "http://h/a", "http://h/b"},
		"--upload up.txt http://h/d/up.txt":              {"-T", "up.txt", "http://h/d/"},
		"--referer auto u":                               {"-e", ";auto", "u"},
		"--no-output-overwrite -o f u":                   {"--no-clobber", "-o", "f", "u"},
		"--log e.txt --help":                             {"--stderr", "e.txt", "-h"},
		"--tls-ciphers TLS_AES_128_GCM_SHA256 --version": {"--tls13-ciphers", "TLS_AES_128_GCM_SHA256", "-V"},
	} {
		got := strings.TrimPrefix(native(t, FromCurl, args...), ShellQuote(curlSpec.defaults)+" ")
		if got != want {
			t.Errorf("%q:\n got %s\nwant %s", args, got, want)
		}
	}
	if _, err := FromCurl([]string{"-e", "http://r/;auto", "u"}); err == nil {
		t.Error("-e URL;auto: no error")
	}
}

// curl sends Accept: */* unless -H sets another; --json sets
// application/json.
func TestCurl_SendsAcceptAll(t *testing.T) {
	last := func(o *config.Options) string {
		v := ""
		for _, h := range o.Headers {
			if k, val, _ := config.ParseHeader(h); strings.EqualFold(k, "Accept") {
				v = val
			}
		}
		return v
	}
	for want, args := range map[string][]string{
		"*/*":              {"u"},
		"text/html":        {"-H", "Accept: text/html", "u"},
		"application/json": {"--json", "{}", "u"},
	} {
		if got := last(curlOptions(t, args...)); got != want {
			t.Errorf("%q: Accept %q, want %q", args, got, want)
		}
	}
}
