package config

import "strings"

const (
	formContentType = "Content-Type: application/x-www-form-urlencoded"
	jsonContentType = "Content-Type: application/json"
)

// hasHeader reports whether headers ("Name: value" entries) set name.
func hasHeader(headers []string, name string) bool {
	for _, h := range headers {
		if k, _, ok := strings.Cut(h, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return true
		}
	}
	return false
}

// addDefaultContentType labels a -d, --data-binary or --data-urlencode body
// as a form, as curl does. A GET gets no Content-Type, and a Content-Type
// from -H wins (audit finding M1). -F sets its own multipart type, and -T
// sends none, as in curl.
func addDefaultContentType(opts *Options) {
	if hasHeader(opts.Headers, "Content-Type") {
		return
	}
	if opts.Data != "" || opts.DataBinary != "" || len(opts.DataURLEncode) > 0 {
		opts.Headers = append(opts.Headers, formContentType)
	}
}
