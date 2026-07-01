package config

import "testing"

func TestParseRate(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1000", 1000, true},
		{"2k", 2000, true},
		{"2K", 2000, true},
		{"1m", 1000000, true},
		{"1M", 1000000, true},
		{"1g", 1000000000, true},
		{"1G", 1000000000, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-5", 0, false},
		{"0", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseRate(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("ParseRate(%q) = (%d, %v), want (%d, nil)", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("ParseRate(%q) expected error, got (%d, nil)", tc.in, got)
		}
	}
}
