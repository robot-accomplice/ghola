package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func apply(t *testing.T, args ...string) *Command {
	t.Helper()
	return parse(t, args...)
}

// The v1 defaults (docs/cli/v1-defaults.md) apply with no options:
// redirects up to 20, fail at 400 and above, 3 retries for a GET, and
// decompression.
func TestApply_Defaults(t *testing.T) {
	o, err := Apply(apply(t, "example.com/f"))
	if err != nil {
		t.Fatal(err)
	}
	r := o.Resilience
	if !r.Location || r.MaxRedirs != 20 || r.Retries != 3 || !r.RetryHTTP {
		t.Errorf("resilience = %+v", r)
	}
	if !o.Output.Fail || o.Output.FailsOn(399) || !o.Output.FailsOn(404) || o.Output.FailsOn(304) {
		t.Errorf("the fail rule is not 400 and above: %+v", o.Output)
	}
	if !o.Stealth.Compressed || o.URL != "http://example.com/f" || !strings.HasPrefix(o.Stealth.Agent, "ghola/") {
		t.Errorf("options = %+v", o)
	}
}

// A POST does not retry unless --retry-max says so: a retry repeats its
// effect (v1 defaults, item 6).
func TestApply_PostDoesNotRetry(t *testing.T) {
	o, _ := Apply(apply(t, "-d", "a=1", "u"))
	if o.Method != "POST" || o.Resilience.Retries != 0 {
		t.Errorf("method %s, retries %d", o.Method, o.Resilience.Retries)
	}
	o, _ = Apply(apply(t, "-d", "a=1", "-r", "2", "u"))
	if o.Resilience.Retries != 2 {
		t.Errorf("explicit retries = %d", o.Resilience.Retries)
	}
}

// A resume asks for identity, so the byte range applies to the file
// itself; an explicit --compressed with a resume stays an error.
func TestApply_ResumeTurnsDecompressionOff(t *testing.T) {
	o, err := Apply(apply(t, "-c", "-o", "f", "u"))
	if err != nil || o.Stealth.Compressed || o.Output.ContinueAt != "-" {
		t.Fatalf("o = %+v, err %v", o, err)
	}
	if _, err := Apply(apply(t, "-c", "--compressed", "-o", "f", "u")); err == nil {
		t.Error("--compressed with a resume was accepted")
	}
}

// ghola download saves under the remote name; -o DIR/ puts it in a folder.
func TestApply_DownloadNames(t *testing.T) {
	o, _ := Apply(apply(t, "download", "https://h/dir/file.zip"))
	if o.Output.File != "file.zip" {
		t.Errorf("download name = %q", o.Output.File)
	}
	o, _ = Apply(apply(t, "-o", "dl/", "https://h/dir/file.zip"))
	if o.Output.File != filepath.Join("dl", "file.zip") {
		t.Errorf("-o dl/ name = %q", o.Output.File)
	}
}

// Values with units reach the transfer options.
func TestApply_Units(t *testing.T) {
	o, err := Apply(apply(t, "--timeout", "1.5s", "--timeout-connect", "500ms", "--rate-max", "1M", "-k", "u"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Resilience.Timeout != 1500 || o.Resilience.ConnectTimeout != 500*time.Millisecond || o.Output.LimitRate != "1048576" || !o.Stealth.Insecure {
		t.Errorf("o = %+v", o)
	}
}

// An option of a phase that is not built is refused with its phase, so no
// option is accepted and then ignored.
func TestApply_UnbuiltOptionIsRefused(t *testing.T) {
	for _, args := range [][]string{{"--hsts", "cache.txt", "u"}, {"--doh", "https://dns.example/q", "u"}, {"--http", "2", "u"}, {"--proxy-tls-ca", "x", "u"}} {
		if _, err := Apply(apply(t, args...)); err == nil || !strings.Contains(err.Error(), "not built yet") {
			t.Errorf("%q: err = %v", args, err)
		}
	}
}

// Every option of phase 0B has a setter: the parser build covers them.
func TestApply_Phase0BIsBuilt(t *testing.T) {
	for _, o := range table {
		if o.Phase == "0B" && setters[o.Name] == nil && !callerOptions[o.Name] {
			t.Errorf("--%s is phase 0B but has no setter", o.Name)
		}
	}
}

// --data-literal sends a value that starts with @ as it is; the v1
// --retry-status default has curl's codes (408 and 524 too).
func TestApply_DataLiteralAndRetryStatus(t *testing.T) {
	o, _ := Apply(apply(t, "--data-literal", "@not-a-file", "u"))
	if o.Data != "@not-a-file" || !o.DataLiteral {
		t.Errorf("data = %q, literal %v", o.Data, o.DataLiteral)
	}
	o, _ = Apply(apply(t, "u"))
	if want := []int{408, 429, 500, 502, 503, 504, 522, 524}; !equalInts(o.Resilience.RetryStatus, want) {
		t.Errorf("retry-status = %v, want %v", o.Resilience.RetryStatus, want)
	}
	if _, err := Apply(apply(t, "--retry-status", "429,abc", "u")); err == nil {
		t.Error("a bad status list was accepted")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A negative count is an argument error (audit finding H5).
func TestApply_NegativeCountIsRefused(t *testing.T) {
	for _, args := range [][]string{{"-r", "-1", "u"}, {"--follow-max", "-2", "u"}} {
		if _, err := Apply(apply(t, args...)); err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
}

// --json sets the JSON media types unless -H gives them; --auth-bearer
// is a credential header; --query adds URL-encoded parameters.
func TestApply_RequestShaping(t *testing.T) {
	o, err := Apply(apply(t, "--json", `{"a":1}`, "--auth-bearer", "tok", "--query", "q=a b", "--query", "x=1", "https://h/p?k=v"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Data != `{"a":1}` || o.Method != "POST" {
		t.Errorf("data %q, method %s", o.Data, o.Method)
	}
	for _, want := range []string{"Content-Type: application/json", "Accept: application/json", "Authorization: Bearer tok"} {
		if !contains(o.Headers, want) {
			t.Errorf("headers %q lack %q", o.Headers, want)
		}
	}
	if o.URL != "https://h/p?k=v&q=a+b&x=1" {
		t.Errorf("URL = %q", o.URL)
	}
	o, _ = Apply(apply(t, "-H", "Content-Type: application/vnd.x+json", "--json", "{}", "u"))
	if contains(o.Headers, "Content-Type: application/json") {
		t.Errorf("--json overrode the -H Content-Type: %q", o.Headers)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// URL globs expand by default, as in curl; --no-url-glob keeps them.
func TestApply_URLGlob(t *testing.T) {
	o, err := Apply(apply(t, "https://h/{a,b}/f[1-2]"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://h/a/f1", "https://h/a/f2", "https://h/b/f1", "https://h/b/f2"}; strings.Join(o.URLs, " ") != strings.Join(want, " ") {
		t.Errorf("URLs = %q", o.URLs)
	}
	if o, _ := Apply(apply(t, "--no-url-glob", "https://h/{a,b}")); len(o.URLs) != 1 {
		t.Errorf("--no-url-glob expanded: %q", o.URLs)
	}
	if _, err := Apply(apply(t, "https://h/[3-1]")); err == nil || !strings.Contains(err.Error(), "bad range") {
		t.Errorf("bad glob: err = %v", err)
	}
}

// --auth-netrc-file gives the credentials of the URL's host; -u wins.
func TestApply_Netrc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netrc")
	os.WriteFile(path, []byte("machine h login alice password s3cret\n"), 0o600) //nolint:errcheck
	o, err := Apply(apply(t, "--auth-netrc-file", path, "https://h/f"))
	if err != nil || o.User != "alice:s3cret" {
		t.Fatalf("user %q, err %v", o.User, err)
	}
	if o, _ := Apply(apply(t, "--auth-netrc-file", path, "-u", "bob:x", "https://h/f")); o.User != "bob:x" {
		t.Errorf("-u did not win: %q", o.User)
	}
	if o, _ := Apply(apply(t, "--auth-netrc-file", path, "https://other/f")); o.User != "" {
		t.Errorf("another host got %q", o.User)
	}
}
