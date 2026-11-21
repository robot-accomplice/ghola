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
