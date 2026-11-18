package config

import (
	"fmt"
	"strings"
)

// ParseHeader splits a -H value "Name: value" into its name and value. An
// empty value means: remove the header, as in curl. ok is false when the
// value has no colon or no name.
func ParseHeader(raw string) (name, value string, ok bool) {
	name, value, found := strings.Cut(raw, ":")
	name = strings.TrimSpace(name)
	if !found || name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(value), true
}

// checkInput refuses values that would send a wrong request (audit finding
// M7). Before, a negative --timeout and -n 0 were accepted, and a header
// without a colon was dropped without a message.
func checkInput(opts *Options) error {
	if opts.Resilience.Timeout < 0 {
		return fmt.Errorf("--timeout cannot be negative: %d", opts.Resilience.Timeout)
	}
	if opts.Concurrency < 1 {
		return fmt.Errorf("-n/--connections must be 1 or more: %d", opts.Concurrency)
	}
	for _, h := range opts.Headers {
		if _, _, ok := ParseHeader(h); !ok {
			return fmt.Errorf("invalid header %q: use \"Name: value\", or \"Name:\" to remove the header", h)
		}
	}
	return nil
}
