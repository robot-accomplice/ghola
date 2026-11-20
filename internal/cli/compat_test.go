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
	want := append(append([]string(nil), curlSpec.defaults...), "-q", "--follow", "-H", "X-A: 1", "-o", "file.zip", "-c", "https://h/f")
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

// --create-dirs and --max-filesize apply as the native output rules.
func TestFromCurl_OutputRules(t *testing.T) {
	cmd, err := Parse(fromCurl(t, "--create-dirs", "--max-filesize", "1K", "-o", "d/f", "u").Args)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Apply(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Output.CreateDirs || o.Output.MaxSize != 1024 || o.Output.NoOverwrite {
		t.Errorf("output = %+v", o.Output)
	}
}

// -K reads a config file in curl's syntax.
func TestFromCurl_Config(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.txt")
	os.WriteFile(path, []byte("url = \"https://h/f\"\nlocation\n"), 0o644) //nolint:errcheck
	c := fromCurl(t, "-K", path)
	if got := strings.Join(c.Args[len(curlSpec.defaults):], " "); got != "--follow https://h/f" {
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

func native(t *testing.T, from func([]string) (*Compat, error), args ...string) string {
	t.Helper()
	c, err := from(args)
	if err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	return ShellQuote(c.Args)
}

// wget: without -O, ghola download saves under the remote name; with -O
// it is a fetch, which takes a body. -P and -O join into one -o, --tries
// counts tries, and -T is wget's DNS, connect and read timeout.
func TestFromWget(t *testing.T) {
	cases := map[string][]string{
		"download -r 0 --no-compressed --no-url-glob --auth-netrc --output-create-dirs -o dl/ https://h/f":     {"-P", "dl", "https://h/f"},
		"-r 0 --no-compressed --no-url-glob --auth-netrc -q -d a=1 -o - https://h/e":                           {"-qO-", "--post-data=a=1", "https://h/e"},
		"-r 0 --no-compressed --no-url-glob --auth-netrc -r 2 --output-create-dirs -o dl/f.txt https://h/f":    {"--tries=3", "-P", "dl", "-O", "f.txt", "https://h/f"},
		"download -r 0 --no-compressed --no-url-glob --auth-netrc --timeout-connect 5 --stall-time 5 -u a:b u": {"-T", "5", "--user=a", "--password=b", "u"},
	}
	for want, args := range cases {
		if got := native(t, FromWget, args...); got != want {
			t.Errorf("%q:\n got %s\nwant %s", args, got, want)
		}
	}
	if _, err := FromWget([]string{"-r", "https://h/"}); err == nil || !strings.Contains(err.Error(), "ghola mirror") {
		t.Errorf("-r: err = %v", err)
	}
}

// aria2c: -x and -s are connections, -d and -o join, --max-tries counts
// tries, --timeout is a read timeout, and tuning options such as -k are
// accepted and dropped.
func TestFromAria2c(t *testing.T) {
	got := native(t, FromAria2c, "-x16", "-s16", "-k1M", "-d", "dl", "-o", "f.iso", "--max-tries=2", "--timeout=3", "https://h/f.iso")
	want := "download -r 4 --retry-delay 0 --no-compressed --no-url-glob --auth-netrc -n 16 -n 16 -r 1 --stall-time 3 -o dl/f.iso https://h/f.iso"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	// aria2c keeps an existing file by default, as ghola download does.
	for value, want := range map[string]string{"true": " --output-overwrite u", "false": " u"} {
		if got := native(t, FromAria2c, "--allow-overwrite="+value, "u"); !strings.HasSuffix(got, "--auth-netrc"+want) {
			t.Errorf("--allow-overwrite=%s: got %s", value, got)
		}
	}
}
