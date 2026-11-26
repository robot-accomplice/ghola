package cli

import (
	"regexp"
	"strings"
	"testing"
)

// Every long option with one letter missing gets "Did you mean" with its
// name (plan section 6), so a typo never ends at "option unknown". When
// several options are as near, all of them are named.
func TestParse_TypoOfEveryOption(t *testing.T) {
	for _, o := range table {
		if len(o.Name) < 4 || strings.Contains(o.Name, "*") {
			continue
		}
		i := len(o.Name) - 2
		typo := o.Name[:i] + o.Name[i+1:]
		if byName[typo] != nil {
			continue // the typo is another option
		}
		_, err := Parse([]string{"--" + typo, "u"})
		if err == nil || !regexp.MustCompile(`Did you mean (--\S+ or )*--`+regexp.QuoteMeta(o.Name)+`( or --\S+)*\?`).MatchString(err.Error()) {
			t.Errorf("--%s: %v", typo, err)
		}
	}
}

// The error for an unknown flag teaches what the user meant, and never a
// guess that misleads (each case was wrong before):
//   - a single unknown letter gets no "Did you mean": --ip is not near -9;
//   - curl --get is -G, ghola --query;
//   - --out is the start of --output, not near --auth;
//   - a v0 letter that curl also has names both meanings;
//   - curl -K is --config, not --input;
//   - aria2c --no-proxy is --proxy-bypass, not "--proxy has no --no- form".
func TestParse_UnknownFlagTeaches(t *testing.T) {
	for args, want := range map[string][]string{
		"-9 u":           {"-9 is not a ghola option. ghola --help lists the options."},
		"--get u":        {"--query"},
		"--out f u":      {"Did you mean --output?"},
		"-D 1 u":         {"--wait-jitter", "curl", "--save-headers"},
		"-G u":           {"v0", "curl", "--query"},
		"-K f u":         {"--config"},
		"--no-proxy x u": {"aria2c", "--proxy-bypass"},
	} {
		_, err := Parse(strings.Fields(args))
		for _, w := range want {
			if err == nil || !strings.Contains(err.Error(), w) {
				t.Errorf("%s: err = %v, want %q", args, err, w)
			}
		}
		if err != nil && strings.HasPrefix(args, "-K") && strings.Contains(err.Error(), "--input") {
			t.Errorf("%s: %v", args, err)
		}
	}
}

// A bad count names the value, not Go's parser.
func TestParse_BadCountSaysWhatIsWrong(t *testing.T) {
	_, err := Parse([]string{"download", "-n", "x", "u"})
	if err == nil || strings.Contains(err.Error(), "strconv") || !strings.Contains(err.Error(), `"x" is not a whole number`) {
		t.Errorf("err = %v", err)
	}
}
