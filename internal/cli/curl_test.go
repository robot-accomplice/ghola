package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fromCurl(t *testing.T, args ...string) *Curl {
	t.Helper()
	c, err := FromCurl(args)
	if err != nil {
		t.Fatalf("FromCurl(%q): %v", args, err)
	}
	return c
}

// curl's defaults come first, so the curl options after them win; letter
// groups and values attached to a letter translate as curl reads them.
func TestFromCurl_Translates(t *testing.T) {
	c := fromCurl(t, "-sSL", "-H", "X-A: 1", "-ofile.zip", "-C", "-", "https://h/f")
	want := append(append([]string(nil), curlDefaults...), "-q", "--follow", "-H", "X-A: 1", "-o", "file.zip", "-c", "https://h/f")
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args =\n%q\nwant\n%q", c.Args, want)
	}
	if c.HideErrors {
		t.Error("-sS hides the errors")
	}
}

// curl -s alone drops the error message, as curl does; -sS keeps it.
func TestFromCurl_SilentHidesErrors(t *testing.T) {
	if !fromCurl(t, "-s", "u").HideErrors {
		t.Error("-s keeps the errors")
	}
}

// The native form parses and applies with curl's defaults: no redirects,
// no fail, no retries, no decompression.
func TestFromCurl_RunsWithCurlDefaults(t *testing.T) {
	c := fromCurl(t, "https://h/f")
	cmd, err := Parse(c.Args)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	r := o.Resilience
	if r.Location || o.Output.Fail || r.Retries != 0 || o.Stealth.Compressed || r.MaxRedirs != 50 {
		t.Errorf("not curl's defaults: %+v, fail %v, compressed %v", r, o.Output.Fail, o.Stealth.Compressed)
	}
}

// -K reads a config file in curl's syntax.
func TestFromCurl_Config(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.txt")
	os.WriteFile(path, []byte("url = \"https://h/f\"\nlocation\n"), 0o644) //nolint:errcheck
	c := fromCurl(t, "-K", path)
	if got := strings.Join(c.Args[len(curlDefaults):], " "); got != "https://h/f --follow" {
		t.Errorf("args = %q", got)
	}
}

// An option that ghola curl cannot run is an argument error that says
// what it maps to, so nothing runs with a dropped option.
func TestFromCurl_Unsupported(t *testing.T) {
	for args, want := range map[string]string{
		"--http2":     "maps to --http",
		"--libcurl x": "is not supported",
	} {
		_, err := FromCurl(strings.Fields(args))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
}

// --show-native prints the native command, quoted for a shell.
func TestFromCurl_ShowNative(t *testing.T) {
	c := fromCurl(t, "--show-native", "-H", "X-A: 1", "u")
	if !c.ShowNative || !strings.HasSuffix(ShellQuote(c.Args), "-H 'X-A: 1' u") {
		t.Errorf("%v %q", c.ShowNative, ShellQuote(c.Args))
	}
}
