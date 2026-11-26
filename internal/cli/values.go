// Package cli is the v1 command line (docs/cli-v1.md): the jobs, the
// options of docs/cli/v1-options.csv, the value syntax, and the errors that
// teach. The option table is generated from the CSV (table_gen.go), so the
// spec and the parser cannot drift: a test compares them.
package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Option is one row of the option table.
type Option struct {
	Name    string
	Short   byte // 0 when the option has no letter
	Group   string
	Scope   string // all, fetch, download, mirror
	Type    string
	Default string
	Curl    string
	Wget    string
	Aria2   string
	Phase   string
	Help    string
}

// V0Flag is one row of the v0-to-v1 mapping.
type V0Flag struct {
	V0   string
	V1   string
	Note string
}

// The types that the parser checks. Other types are free text.
const (
	typeBool     = "bool"
	typeInt      = "int"
	typeDuration = "duration"
	typeSize     = "size"
	typeRate     = "rate"
	typeTopic    = "topic" // --help and --help=topic: the value is optional
)

// sizeUnits are the size suffixes (v1 spec, section 3; question C, option
// A): K, M, G and T are 1024-based, as in curl, wget and aria2; kB, MB, GB
// and TB are 1000-based; KiB, MiB, GiB and TiB are 1024-based.
var sizeUnits = map[string]float64{
	"": 1, "b": 1,
	"k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
}

// ParseSize reads a size such as 500, 2M, 1.5GiB or 10kB, in bytes.
func ParseSize(s string) (int64, error) {
	num, unit := splitNumber(s)
	mult, ok := sizeUnits[strings.ToLower(unit)]
	if !ok {
		return 0, fmt.Errorf("%q: unknown size unit %q (use B, K, M, G, kB, MB, KiB, MiB)", s, unit)
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	return int64(v * mult), nil
}

// ParseRate reads a rate such as 2M or 2M/s, in bytes per second.
func ParseRate(s string) (int64, error) {
	return ParseSize(strings.TrimSuffix(strings.TrimSuffix(s, "/s"), "/S"))
}

// ParseDuration reads a duration such as 500ms, 30s, 2m or 1h. A bare
// number is seconds, as in curl.
func ParseDuration(s string) (time.Duration, error) {
	num, unit := splitNumber(s)
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q is not a duration (use 500ms, 30s, 2m, 1h)", s)
	}
	mult := map[string]time.Duration{"": time.Second, "ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour}
	m, ok := mult[unit]
	if !ok {
		return 0, fmt.Errorf("%q: unknown time unit %q (use ms, s, m, h)", s, unit)
	}
	return time.Duration(v * float64(m)), nil
}

// splitNumber splits "1.5GiB" into "1.5" and "GiB".
func splitNumber(s string) (string, string) {
	i := 0
	for i < len(s) && (s[i] == '.' || ('0' <= s[i] && s[i] <= '9')) {
		i++
	}
	return s[:i], s[i:]
}

// checkValue checks a value against the option type.
func checkValue(o *Option, v string) error {
	var err error
	switch o.Type {
	case typeInt:
		err = checkCount(v)
	case typeDuration:
		_, err = ParseDuration(v)
	case typeSize:
		_, err = ParseSize(v)
	case typeRate:
		_, err = ParseRate(v)
	default:
		return checkEnum(o, v)
	}
	if err != nil {
		return fmt.Errorf("--%s: %w", o.Name, err)
	}
	return nil
}

// checkEnum checks a value of a type such as "url|header". A type with
// spaces ("text | @file | @-") describes the value and is not a list.
func checkEnum(o *Option, v string) error {
	if !strings.Contains(o.Type, "|") || strings.Contains(o.Type, " ") {
		return nil
	}
	for _, choice := range strings.Split(o.Type, "|") {
		if v == choice || isPlaceholder(choice) {
			return nil // a placeholder such as URL in "none|auto|URL" takes any value
		}
	}
	return fmt.Errorf("--%s: %q is not one of %s", o.Name, v, strings.ReplaceAll(o.Type, "|", ", "))
}

// isPlaceholder reports a choice that stands for a value, not a word. Only
// URL is one: PEM|DER|P12 are words in capitals.
func isPlaceholder(choice string) bool {
	return choice == "URL"
}

// checkCount checks a whole number, with a message that names the value.
func checkCount(v string) error {
	if _, err := strconv.Atoi(v); err != nil {
		return fmt.Errorf("%q is not a whole number", v)
	}
	return nil
}
