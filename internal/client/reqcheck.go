package client

import (
	"fmt"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"golang.org/x/net/http/httpguts"
)

// checkRequest refuses a request value that cannot go on the wire, before
// the first attempt. Before, the stack refused it on each attempt, so it
// was retried and exited 55 or 56; and a line break in -H or the
// User-Agent became a space without a message.
func checkRequest(opts *config.Options) error {
	if v := opts.Stealth.HTTPVersion; (v == "3" || v == "3-only") && opts.Stealth.Impersonate == "" {
		return fmt.Errorf("%w: --http %s needs --impersonate: the default stack has no HTTP/3", ErrInvalidRequest, v)
	}
	if m := opts.Method; m != "" && !httpguts.ValidHeaderFieldName(m) {
		return fmt.Errorf("%w: method %q is not a token", ErrInvalidRequest, m)
	}
	if strings.ContainsFunc(opts.URL, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return fmt.Errorf("%w: the URL %q holds a space or a control byte", ErrInvalidRequest, opts.URL)
	}
	if !httpguts.ValidHeaderFieldValue(opts.Stealth.Agent) {
		return fmt.Errorf("%w: the User-Agent %q holds a control byte", ErrInvalidRequest, opts.Stealth.Agent)
	}
	for _, h := range opts.Headers {
		name, value, ok := config.ParseHeader(h)
		if !ok {
			continue // checkInput refused it already
		}
		if !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("%w: header name %q is not a token", ErrInvalidRequest, name)
		}
		if !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("%w: header %q holds a control byte", ErrInvalidRequest, name)
		}
	}
	return nil
}
