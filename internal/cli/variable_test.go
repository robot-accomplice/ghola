package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only the --expand-NAME form expands {{name}} (spec decision I, curl's
// rule): a header or a body that holds {{ must reach the server as it is.
func TestParse_ExpandOnlyInExpandForm(t *testing.T) {
	c, err := Parse([]string{"--variable", "tok=abc", "--expand-header", "A: {{tok}}", "-H", "B: {{tok}}", "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.Values["header"], "|")
	if got != "A: abc|B: {{tok}}" {
		t.Errorf("headers %q, want the expand form expanded and the plain form as it is", got)
	}
}

func TestVariables(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "v")
	if err := os.WriteFile(file, []byte("  from file \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOLA_TEST_VAR", "env value")
	v := variables{}
	for _, arg := range []string{"a=x y&z", "q=say \"hi\"", "f@" + file, "%GHOLA_TEST_VAR", "%GHOLA_TEST_UNSET_VAR=fallback"} {
		if err := v.set(arg); err != nil {
			t.Fatal(arg, err)
		}
	}
	for in, want := range map[string]string{
		"{{a}}":                    "x y&z",
		"{{a:url}}":                "x+y%26z",
		"{{f:trim}}":               "from file",
		"{{f:trim:b64}}":           "ZnJvbSBmaWxl",
		`{{q:json}}`:               `say \"hi\"`,
		"{{GHOLA_TEST_VAR}}":       "env value",
		"{{GHOLA_TEST_UNSET_VAR}}": "fallback",
		`\{{a}}`:                   "{{a}}",
	} {
		if got, err := v.expand(in); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	// A name that is not set, or a function that does not exist, is an
	// error: a typo must not send an empty value.
	for _, in := range []string{"{{nope}}", "{{a:upper}}"} {
		if _, err := v.expand(in); err == nil {
			t.Errorf("%s: no error", in)
		}
	}
	for _, arg := range []string{"bad-name=1", "%GHOLA_TEST_UNSET_VAR", "novalue"} {
		if err := (variables{}).set(arg); err == nil {
			t.Errorf("--variable %s: no error", arg)
		}
	}
}

// A variable must come before the option that uses it, as in curl.
func TestParse_VariableOrder(t *testing.T) {
	if _, err := Parse([]string{"--expand-header", "A: {{tok}}", "--variable", "tok=abc", "http://x"}); err == nil {
		t.Error("a variable set after its use was accepted")
	}
}

// curl's --expand-data and --variable run unchanged with ghola curl.
func TestFromCurl_Expand(t *testing.T) {
	cp, err := FromCurl([]string{"--variable", "n=5", "--expand-data", "count={{n}}", "http://h/"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(cp.Args)
	if err != nil {
		t.Fatalf("%v: %v", cp.Args, err)
	}
	if c.Last("data") != "count=5" {
		t.Errorf("data %q from %v", c.Last("data"), cp.Args)
	}
}
