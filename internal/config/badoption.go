package config

import (
	"errors"
	"fmt"
)

// ErrBadOption marks an option value that the transfer code refuses before
// it sends a request: a bad --tls-pin, --resolve or --proxy, or an option
// that does not work with another one. It is an argument error, so the CLI
// exits 2 (BadFlag), as curl does for a bad option value.
var ErrBadOption = errors.New("bad option")

// BadOptionf returns an error with the formatted text that matches
// ErrBadOption with errors.Is. The text names the option; it does not
// start with "bad option". A %w verb in format wraps that error too.
func BadOptionf(format string, args ...any) error {
	return badOptionError{fmt.Errorf(format, args...)}
}

type badOptionError struct{ err error }

func (e badOptionError) Error() string        { return e.err.Error() }
func (e badOptionError) Unwrap() error        { return e.err }
func (e badOptionError) Is(target error) bool { return target == ErrBadOption }
