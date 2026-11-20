package cli

import (
	"encoding/csv"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The generated table must match the spec data. A change to the CSV
// without "python scripts/cli_spec.py" fails here, so the parser cannot
// drift from the spec.
func TestTable_MatchesTheSpecCSV(t *testing.T) {
	f, err := os.Open("../../docs/cli/v1-options.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows)-1 != len(table) {
		t.Fatalf("the CSV has %d options and the table %d: run python scripts/cli_spec.py", len(rows)-1, len(table))
	}
	for i, r := range rows[1:] {
		o := table[i]
		short := ""
		if o.Short != 0 {
			short = "-" + string(o.Short)
		}
		if r[0] != o.Name || r[1] != short || r[4] != o.Type || r[5] != o.Default {
			t.Errorf("row %d: CSV %q %q %q %q, table %q %q %q %q", i+1, r[0], r[1], r[4], r[5], o.Name, short, o.Type, o.Default)
		}
	}
}

func parse(t *testing.T, args ...string) *Command {
	t.Helper()
	c, err := Parse(args)
	if err != nil {
		t.Fatalf("Parse(%q): %v", args, err)
	}
	return c
}

func parseErr(t *testing.T, args ...string) string {
	t.Helper()
	_, err := Parse(args)
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("Parse(%q): err = %v, want a UsageError", args, err)
	}
	return ue.Msg
}

// Letters group, a value can follow a letter, and a letter on a boolean
// flips its default: -q turns quiet on, -k turns tls-verify off.
func TestParse_Letters(t *testing.T) {
	c := parse(t, "-qko", "out.zip", "https://h/f")
	if c.Last("quiet") != "true" || c.Last("tls-verify") != "false" || c.Last("output") != "out.zip" {
		t.Errorf("values = %v", c.Values)
	}
	if !reflect.DeepEqual(c.Args, []string{"https://h/f"}) {
		t.Errorf("args = %q", c.Args)
	}
	if d := parse(t, "download", "-n8", "u"); d.Job != JobDownload || d.Last("connections") != "8" {
		t.Errorf("-n8: %v", d.Values)
	}
}

// Every boolean has a --no- form and takes =true or =false.
func TestParse_Booleans(t *testing.T) {
	c := parse(t, "--no-follow", "--quiet=false", "--compressed", "u")
	if c.Last("follow") != "false" || c.Last("quiet") != "false" || c.Last("compressed") != "true" {
		t.Errorf("values = %v", c.Values)
	}
	if msg := parseErr(t, "--no-output", "f"); !strings.Contains(msg, "no --no- form") {
		t.Errorf("--no-output: %q", msg)
	}
}

// Values are checked: units for durations and sizes, and the choices of
// a list type.
func TestParse_Values(t *testing.T) {
	parse(t, "--timeout", "30s", "--rate-max=2M/s", "--max-size", "1GiB", "--output-name", "header", "u")
	for _, args := range [][]string{
		{"--timeout", "3x", "u"}, {"--max-size", "lots", "u"}, {"--output-name", "maybe", "u"}, {"-r", "many", "u"},
	} {
		parseErr(t, args...)
	}
	if d, _ := ParseDuration("30"); d != 30*time.Second {
		t.Errorf("a bare number is %v, want seconds", d)
	}
	for in, want := range map[string]int64{"2M": 2 << 20, "10kB": 10000, "1GiB": 1 << 30, "500": 500} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}

// An option outside its jobs is refused with the job where it works.
func TestParse_Scope(t *testing.T) {
	if msg := parseErr(t, "-n", "4", "u"); !strings.Contains(msg, "only with ghola download") {
		t.Errorf("-n on fetch: %q", msg)
	}
	if msg := parseErr(t, "serve", "-o", "x"); !strings.Contains(msg, "does not apply to ghola serve") {
		t.Errorf("-o on serve: %q", msg)
	}
	parse(t, "serve", "--help") // general options work everywhere
}

// The errors teach the v1 form (v1 spec, section 7).
func TestParse_ErrorsTeach(t *testing.T) {
	cases := map[string][]string{
		"--serve is a ghola v0 flag. In v1 it is ghola serve.": {"--serve"},
		"-x means different things in other tools: in curl it is ghola --proxy; in wget it is ghola --output-create-dirs; in aria2c it is ghola --connections (-n)": {"-x", "http://p"},
		"--proxy-insecure is a curl flag":                                                                  {"--proxy-insecure"},
		"--max-filesize is a curl flag. In ghola it is --max-size":                                         {"--max-filesize", "1"},
		"--no-check-certificate is a wget flag. In ghola it is --tls-verify (-k), and it is on by default": {"--no-check-certificate"},
		"--folow is not a ghola option. Did you mean --follow?":                                            {"--folow"},
	}
	for want, args := range cases {
		if msg := parseErr(t, args...); !strings.HasPrefix(msg, want) {
			t.Errorf("%q:\n got %q\nwant %q...", args, msg, want)
		}
	}
}

// A compatibility command keeps its arguments as they are, and -- ends the
// options.
func TestParse_CompatAndOperands(t *testing.T) {
	if c := parse(t, "curl", "-sSL", "-o", "f", "u"); c.Job != JobCurl || !reflect.DeepEqual(c.Args, []string{"-sSL", "-o", "f", "u"}) {
		t.Errorf("curl: %+v", c)
	}
	if c := parse(t, "-q", "--", "-not-an-option"); !reflect.DeepEqual(c.Args, []string{"-not-an-option"}) {
		t.Errorf("--: %q", c.Args)
	}
}

// --proxy-tls-* takes the type of the --tls-* option.
func TestParse_ProxyTLS(t *testing.T) {
	if c := parse(t, "--proxy-tls-ca", "ca.pem", "--no-proxy-tls-verify", "u"); c.Last("proxy-tls-ca") != "ca.pem" || c.Last("proxy-tls-verify") != "false" {
		t.Errorf("values = %v", c.Values)
	}
}

// inspect makes a request as a fetch does, so it takes the fetch options.
func TestParse_InspectTakesFetchOptions(t *testing.T) {
	if c := parse(t, "inspect", "--impersonate", "chrome", "-r", "0", "u"); c.Last("impersonate") != "chrome" {
		t.Errorf("values = %v", c.Values)
	}
	parseErr(t, "inspect", "-n", "4", "u") // a download option stays out
}
