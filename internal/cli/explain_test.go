package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every exit code that ghola uses has a ghola explain message (plan
// section 6): the test reads the ExitCode constants of config.go, so a
// new code without a message fails it.
func TestExplain_EveryExitCode(t *testing.T) {
	src, err := os.ReadFile("../config/config.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := regexp.MustCompile(`ExitCode = (\d+)`).FindAllStringSubmatch(string(src), -1)
	if len(codes) < 20 {
		t.Fatalf("found %d exit codes in config.go; the pattern is wrong", len(codes))
	}
	for _, m := range codes {
		text, err := Explain(m[1])
		if err != nil || !strings.HasPrefix(text, m[1]+": ") {
			t.Errorf("ghola explain %s: %q, %v", m[1], text, err)
		}
	}
	if _, err := Explain("4"); err == nil {
		t.Error("a code that ghola does not use was explained")
	}
}
