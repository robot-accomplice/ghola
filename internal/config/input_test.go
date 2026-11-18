package config

import (
	"strings"
	"testing"
)

// TestParseFlags_RejectsBadInput checks the input checks of audit finding
// M7. Each value used to be accepted: a negative timeout and -n 0 sent the
// request anyway, and a header without a colon was dropped without a word.
// An argument error stops ghola before any request, so a typo cannot reach
// the server.
func TestParseFlags_RejectsBadInput(t *testing.T) {
	tests := []struct {
		args []string
		want string // a part of the error message
	}{
		{[]string{"--timeout", "-5", "http://x"}, "--timeout"},
		{[]string{"-n", "0", "http://x"}, "-n"},
		{[]string{"-n", "-2", "http://x"}, "-n"},
		{[]string{"-H", "NoColonHere", "http://x"}, "NoColonHere"},
		{[]string{"-H", ": no name", "http://x"}, ": no name"},
	}
	for _, tc := range tests {
		_, _, err := ParseFlags(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseFlags(%q) error = %v, want one that names %q", tc.args, err, tc.want)
		}
	}
}

// TestParseFlags_AcceptsHeaderRemoval checks that -H "Name:" is valid: it
// removes the header, as in curl.
func TestParseFlags_AcceptsHeaderRemoval(t *testing.T) {
	if _, _, err := ParseFlags([]string{"-H", "User-Agent:", "http://x"}); err != nil {
		t.Errorf("-H \"User-Agent:\" is refused: %v", err)
	}
}
