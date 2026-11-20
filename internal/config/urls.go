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
	return defaultScheme + "://" + u
}

// defaultScheme is the scheme of a URL without one, as in curl.
const defaultScheme = "http"

// checkOneOutput refuses a second URL where two bodies would share one
// output. The v1 spec pairs -o with the URLs: one -o DIR/ (or the remote
// name of ghola download) serves all, or each URL has its own -o
// (PerURL). One -o FILE and one --har file take one URL.
func checkOneOutput(opts *Options) error {
	if len(opts.URLs) < 2 {
		return nil
	}
	o := opts.Output
	switch {
	case o.HAR != "":
		return fmt.Errorf("--har takes one URL: got %d URLs", len(opts.URLs))
	case o.File != "" && !o.RemoteName && len(o.PerURL) == 0:
		return fmt.Errorf("%d URLs and one -o FILE: give one -o for each URL, or one -o DIR/ for all", len(opts.URLs))
	}
	return nil
}
