// Package config — rate.go provides ParseRate, the single authoritative parser
// for --limit-rate values. Placed here (not in client) so config.ParseFlags can
// validate the flag without importing client (which imports config, creating a
// cycle). client code calls config.ParseRate directly.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRate converts a human-readable rate string into bytes per second.
// Suffixes are decimal: k/K=1e3, m/M=1e6, g/G=1e9, matching curl semantics.
// Returns an error for empty input, non-numeric input, or a value <= 0.
func ParseRate(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty rate")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1_000, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1_000_000, s[:len(s)-1]
	case 'g', 'G':
		mult, s = 1_000_000_000, s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid rate %q", s)
	}
	return n * mult, nil
}
