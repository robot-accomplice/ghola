package config

import (
	"strings"
	"testing"
	"time"
)

// --max-time is curl's name and unit (seconds, decimals allowed) for the
// limit that --timeout sets in milliseconds.
func TestParseFlags_MaxTimeSetsTheTimeout(t *testing.T) {
	opts, _, err := ParseFlags([]string{"--max-time", "1.5", "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Resilience.Timeout != 1500 {
		t.Errorf("Timeout = %d ms, want 1500", opts.Resilience.Timeout)
	}
}

// Two flags for one limit would leave the user unsure which one applies.
func TestParseFlags_MaxTimeAndTimeoutConflict(t *testing.T) {
	_, _, err := ParseFlags([]string{"--max-time", "2", "--timeout", "3000", "http://example.com"})
	if err == nil || !strings.Contains(err.Error(), "use one") {
		t.Fatalf("err = %v, want the conflict error", err)
	}
}

func TestParseFlags_ConnectTimeout(t *testing.T) {
	opts, _, err := ParseFlags([]string{"--connect-timeout", "0.25", "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Resilience.ConnectTimeout != 250*time.Millisecond {
		t.Errorf("ConnectTimeout = %v, want 250ms", opts.Resilience.ConnectTimeout)
	}
}

// curl fills in the other speed flag: --speed-time defaults to 30 s, and
// --speed-limit to 1 byte per second.
func TestParseFlags_SpeedDefaultsAsCurl(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		limit int64
		secs  time.Duration
	}{
		{[]string{"--speed-limit", "500"}, 500, 30 * time.Second},
		{[]string{"--speed-time", "5"}, 1, 5 * time.Second},
		{[]string{"--speed-limit", "500", "--speed-time", "5"}, 500, 5 * time.Second},
		{nil, 0, 0},
	} {
		opts, _, err := ParseFlags(append(tc.args, "http://example.com"))
		if err != nil {
			t.Fatal(err)
		}
		if opts.Resilience.SpeedLimit != tc.limit || opts.Resilience.SpeedTime != tc.secs {
			t.Errorf("%v: SpeedLimit = %d, SpeedTime = %v; want %d, %v",
				tc.args, opts.Resilience.SpeedLimit, opts.Resilience.SpeedTime, tc.limit, tc.secs)
		}
	}
}

// A negative limit is an input error, not "no limit".
func TestParseFlags_NegativeLimitsAreRejected(t *testing.T) {
	for _, flag := range []string{"--max-time", "--connect-timeout", "--speed-limit", "--speed-time"} {
		if _, _, err := ParseFlags([]string{flag, "-1", "http://example.com"}); err == nil {
			t.Errorf("%s -1: no error", flag)
		}
	}
}

// -r -1 is an input error before any request (audit finding H5).
func TestParseFlags_NegativeRetryIsRejected(t *testing.T) {
	if _, _, err := ParseFlags([]string{"-r", "-1", "http://example.com"}); err == nil {
		t.Fatal("-r -1: no error")
	}
}
