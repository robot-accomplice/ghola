package cli

import (
	"bytes"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// The header follows the value: a date gives If-Modified-Since, a leading
// "-" gives If-Unmodified-Since, and -H wins. A value that is neither a
// file nor a date sends no condition and warns, unless -q.
func TestApplyCondTime(t *testing.T) {
	tests := []struct {
		value, header string
		headers       []string
		silent, warns bool
	}{
		{value: "2026-01-02", header: "If-Modified-Since: Fri, 02 Jan 2026 00:00:00 GMT"},
		{value: "-Fri, 02 Jan 2026 00:00:00 GMT", header: "If-Unmodified-Since: Fri, 02 Jan 2026 00:00:00 GMT"},
		{value: "2026-01-02", headers: []string{"if-modified-since: x"}, header: "if-modified-since: x"},
		{value: "no-such-file", warns: true},
		{value: "no-such-file", silent: true},
	}
	for _, tc := range tests {
		o := &config.Options{Headers: tc.headers, Output: config.OutputOptions{Silent: tc.silent}}
		var warn bytes.Buffer
		applyCondTime(o, tc.value, &warn)
		got := ""
		if len(o.Headers) > 0 {
			got = o.Headers[len(o.Headers)-1]
		}
		if got != tc.header || len(o.Headers) > len(tc.headers)+1 {
			t.Errorf("%q: headers %q, want %q", tc.value, o.Headers, tc.header)
		}
		if (warn.Len() > 0) != tc.warns {
			t.Errorf("%q: warning %q", tc.value, warn.String())
		}
		if o.Output.CondTime != (tc.header != "" && len(tc.headers) == 0) {
			t.Errorf("%q: CondTime = %v", tc.value, o.Output.CondTime)
		}
	}
}
