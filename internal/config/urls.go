package config

import (
	"fmt"
	"strings"
)

// takeURLs sets opts.URLs from the arguments, in order, and opts.URL to the
// first one (audit finding M9). A URL without a scheme gets http://, as in
// curl. A chain shortcut can give the URL when no argument does.
func takeURLs(opts *Options, args []string) {
	for _, a := range args {
		opts.URLs = append(opts.URLs, withScheme(a))
	}
	if len(opts.URLs) > 0 {
		opts.URL = opts.URLs[0]
	}
}

// withScheme returns u with http:// in front when it has no scheme.
// url.Parse cannot decide this: it reads "localhost:8080" as the scheme
// "localhost".
func withScheme(u string) string {
	if strings.Contains(u, "://") {
		return u
	}
	return "http://" + u
}

// checkOneOutput refuses a second URL with an option that names one output.
// Two bodies cannot share one file name, and the v1 flag design decides how
// -o pairs with several URLs (curl takes one -o for each URL).
func checkOneOutput(opts *Options) error {
	if len(opts.URLs) < 2 {
		return nil
	}
	o := opts.Output
	if o.File != "" || o.RemoteName || o.WgetMode || o.RemoteHeader || o.HAR != "" {
		return fmt.Errorf("-o, -O, -w, -J and --har take one URL: got %d URLs", len(opts.URLs))
	}
	return nil
}
