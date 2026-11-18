//go:build parity

package parity

// known lists the scenarios that ghola fails today, with the finding that
// explains each failure. The list is a ratchet:
//
//   - A scenario in this list that ghola now passes fails the test. Remove
//     it from the list in the same change as the fix.
//   - A scenario that ghola fails and that is not in this list fails the test.
//
// So a fix cannot land without proof, and a regression cannot land at all.
// The findings are in docs/audits/2026-11-12-v0.7.0-vs-curl-aria2-wget.md.
// Recorded against v0.8.0 on 2026-11-15.
var known = map[string]string{
	"exit_refused": "M6: every network error exits 2",
	"exit_dns":     "M6: every network error exits 2",
	"exit_tls":     "M6: every network error exits 2",
	"exit_timeout": "M6: every network error exits 2",

	"neg_timeout":      "M7: a negative --timeout is accepted",
	"zero_connections": "M7: -n 0 is accepted",
	"header_no_colon":  "M7: a header without a colon is dropped without a message",
	"header_remove":    `M7: -H "User-Agent:" sends the engine default (Go-http-client/1.1)`,

	"proxy_env": "M8: HTTP_PROXY is ignored",

	"url_no_scheme": "M9: a URL without a scheme fails",
	"two_urls":      "M9: extra URLs are ignored without a message",

	"resume_changed": "GAP-if-range: resume does not send If-Range (no tested tool does)",
}
