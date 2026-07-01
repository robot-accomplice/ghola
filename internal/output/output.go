// Package output handles rendering HTTP responses to stdout or files,
// and the snoop-mode security posture report.
package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// ErrNon2xx is returned when --fail is set and the server responds with a non-2xx status.
var ErrNon2xx = errors.New("non-2xx http status")

// ProcessResponse writes the HTTP response to the appropriate destination
// (file or stdout) based on opts. It returns a non-nil error if file I/O fails.
func ProcessResponse(w io.Writer, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response, elapsed time.Duration) error {
	httpOk := rsp.StatusCode() >= 200 && rsp.StatusCode() < 300

	if opts.Output.Fail && !httpOk {
		return fmt.Errorf("%w: %d %s", ErrNon2xx, rsp.StatusCode(), fasthttp.StatusMessage(rsp.StatusCode()))
	}

	body := rsp.Body()

	// Decode gzip when --compressed was requested and the server honoured it.
	if opts.Stealth.Compressed && bytes.EqualFold(rsp.Header.Peek("Content-Encoding"), []byte("gzip")) {
		if decoded, err := rsp.BodyGunzip(); err == nil {
			body = decoded
		}
	}

	// Apply JSON extraction if --jq is set.
	if opts.Output.JQ != "" {
		result := gjson.GetBytes(body, opts.Output.JQ)
		if !result.Exists() {
			return fmt.Errorf("jq path %q: no match in response", opts.Output.JQ)
		}
		body = []byte(result.String())
	}

	if opts.Output.File != "" {
		if err := os.WriteFile(opts.Output.File, body, 0644); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		if opts.Output.Timing {
			fmt.Fprintf(w, "Total: %s\n", elapsed.Truncate(time.Millisecond))
		}
		return nil
	}

	if opts.Output.Silent {
		return nil
	}

	if opts.Output.Include {
		fmt.Fprintf(w, "%s\n\n", bytes.Trim(rsp.Header.Header(), "\n"))
	}
	if opts.Output.Verbose {
		fmt.Fprintf(w, "Ghost Signature: %s\n", req.Header.Peek("X-Ghola-Identity"))
		if opts.Stealth.Impersonate != "" {
			fmt.Fprintf(w, "Profile: %s\n", opts.Stealth.Impersonate)
			fmt.Fprintln(w, "Transport: tls-client (pure Go)")
		}
		if opts.Stealth.StealthHeaders {
			fmt.Fprintln(w, "Stealth Headers: enabled")
		}
		if opts.ProxyCfg.Proxy != "" {
			fmt.Fprintf(w, "Proxy: %s\n", opts.ProxyCfg.Proxy)
		}
		if opts.Stealth.CookieJar != "" {
			fmt.Fprintf(w, "Cookie Jar: %s\n", opts.Stealth.CookieJar)
		}
	}
	fmt.Fprintf(w, "%s\n", bytes.Trim(body, "\n"))

	if opts.Output.Timing {
		fmt.Fprintf(w, "Total: %s\n", elapsed.Truncate(time.Millisecond))
	}

	return nil
}

// RunSnoop prints a security posture report for the target URL.
func RunSnoop(w io.Writer, opts *config.Options, rsp *fasthttp.Response) {
	fmt.Fprintln(w, "--- [Ghola Snoop Mode] ---")
	fmt.Fprintf(w, "Target: %s\n", opts.URL)
	fmt.Fprintf(w, "Status: %d %s\n", rsp.StatusCode(), fasthttp.StatusMessage(rsp.StatusCode()))
	fmt.Fprintf(w, "Content Length: %d bytes\n", rsp.Header.ContentLength())
	fmt.Fprintf(w, "Server: %s\n", rsp.Header.Peek("Server"))
	fmt.Fprintln(w)

	// Security headers section.
	snoopSection(w, "Security Headers", rsp, []headerCheck{
		{"Strict-Transport-Security", "HSTS"},
		{"Content-Security-Policy", "CSP"},
		{"X-Frame-Options", "X-Frame-Options"},
		{"X-Content-Type-Options", "X-Content-Type-Options"},
		{"Referrer-Policy", "Referrer-Policy"},
		{"Permissions-Policy", "Permissions-Policy"},
	})

	// CORS section.
	snoopSection(w, "CORS", rsp, []headerCheck{
		{"Access-Control-Allow-Origin", "Allow-Origin"},
		{"Access-Control-Allow-Methods", "Allow-Methods"},
		{"Access-Control-Allow-Headers", "Allow-Headers"},
	})

	// Rate limiting section.
	snoopSection(w, "Rate Limiting", rsp, []headerCheck{
		{"X-RateLimit-Limit", "Limit"},
		{"X-RateLimit-Remaining", "Remaining"},
		{"X-RateLimit-Reset", "Reset"},
		{"RateLimit-Limit", "Limit (std)"},
		{"RateLimit-Remaining", "Remaining (std)"},
		{"Retry-After", "Retry-After"},
	})

	// WAF/CDN detection.
	fmt.Fprintln(w, "[WAF/CDN Detection]")
	snoopDetect(w, "Cloudflare", rsp, []string{"Cf-Ray", "Cf-Cache-Status"}, "cloudflare")
	snoopDetect(w, "Akamai", rsp, []string{"X-Akamai-Transformed", "X-Akamai-Session-Info"}, "")
	snoopDetect(w, "AWS", rsp, []string{"X-Amzn-Requestid", "X-Amz-Cf-Id", "X-Amz-Cf-Pop"}, "")
	snoopDetect(w, "Fastly", rsp, []string{"X-Served-By", "X-Cache", "X-Cache-Hits"}, "")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "--- [Snoop End] ---")
}

type headerCheck struct {
	Header string
	Label  string
}

func snoopSection(w io.Writer, title string, rsp *fasthttp.Response, checks []headerCheck) {
	var found []string
	for _, c := range checks {
		val := string(rsp.Header.Peek(c.Header))
		if val != "" {
			found = append(found, fmt.Sprintf("  %-30s %s", c.Label+":", val))
		}
	}
	if len(found) > 0 {
		fmt.Fprintf(w, "[%s]\n", title)
		for _, line := range found {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w)
	}
}

func snoopDetect(w io.Writer, name string, rsp *fasthttp.Response, headers []string, serverMatch string) {
	detected := false
	var evidence []string

	if serverMatch != "" {
		server := strings.ToLower(string(rsp.Header.Peek("Server")))
		if strings.Contains(server, serverMatch) {
			detected = true
			evidence = append(evidence, "server="+serverMatch)
		}
	}

	for _, h := range headers {
		if val := string(rsp.Header.Peek(h)); val != "" {
			detected = true
			evidence = append(evidence, h)
		}
	}

	if detected {
		fmt.Fprintf(w, "  %-12s DETECTED (%s)\n", name+":", strings.Join(evidence, ", "))
	} else {
		fmt.Fprintf(w, "  %-12s not detected\n", name+":")
	}
}
