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
	"xhost_basic":  "H1: -u credentials follow a cross-host redirect",
	"xhost_bearer": "H1: a custom Authorization header follows a cross-host redirect",

	"stdout_binary":  "H2: stdout is bytes.Trim'med and gets one newline",
	"stdout_gzip":    "H2: stdout loses the last newline of the decoded body",
	"stdout_verbose": "H2: -v lines go to stdout",

	"timeout_file": "H3: --timeout does not apply to the -o stream",

	"n_post":    "H4: -n repeats the POST",
	"n_refused": "H4: -n drops errors and exits 0",

	"neg_retry": "H5: -r -1 gives a nil response and a panic",

	"redirect_limit": "H6: the redirect limit exits 0",

	"fail_to_file": "H7: -f -o writes the error body",

	"basic_auth": "H8: -u is not sent on the -o download path (found by this harness, 2026-11-15)",

	"get_no_content_type":        "M1: every request gets Content-Type: application/json",
	"get_no_content_type_chrome": "M1: every request gets Content-Type: application/json",
	"form_content_type":          "M1: -d is labelled application/json",

	"stdout_memory": "M2: stdout buffers the whole body (and H2 adds a byte)",

	"rate_stdout": "M3: --limit-rate does not apply to stdout",

	"progress_not_tty": "M4: the meter prints when stderr is not a terminal",
	"progress_total":   "M4: the total comes from the compressed length",

	"no_head": "M5: each -o download sends HEAD first",

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
