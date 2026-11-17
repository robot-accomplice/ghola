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
	"github.com/robot-accomplice/ghola/internal/transport"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// ErrNon2xx is returned when --fail is set and the server responds with a non-2xx status.
var ErrNon2xx = errors.New("non-2xx http status")

// ProcessResponse writes the HTTP response to the appropriate destination
// (file or stdout) based on opts. The body goes to w exactly as the server
// sent it, without a buffer (audit findings H2 and M2). -i puts the header
// block on w in front of the body, as curl -i does. Metadata (-v lines and
// the timing line) goes to ew. It returns a non-nil error if the body cannot
// be read or written.
func ProcessResponse(w, ew io.Writer, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response, elapsed time.Duration) error {
	httpOk := rsp.StatusCode() >= 200 && rsp.StatusCode() < 300

	if opts.Output.Fail && !httpOk {
		return fmt.Errorf("%w: %d %s", ErrNon2xx, rsp.StatusCode(), fasthttp.StatusMessage(rsp.StatusCode()))
	}

	body, err := bodyReader(opts, rsp)
	if err != nil {
		return err
	}

	// Apply JSON extraction if --jq is set. It needs the whole body.
	if opts.Output.JQ != "" {
		b, err := io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("read response body: %w", err)
		}
		result := gjson.GetBytes(b, opts.Output.JQ)
		if !result.Exists() {
			return fmt.Errorf("jq path %q: no match in response", opts.Output.JQ)
		}
		body = strings.NewReader(result.String())
	}

	if opts.Output.File != "" {
		if err := writeFile(opts.Output.File, body); err != nil {
			return err
		}
		printTiming(ew, opts, elapsed)
		return nil
	}

	if opts.Output.Silent {
		return nil
	}

	if opts.Output.Verbose {
		printVerbose(ew, opts, req)
	}
	if opts.Output.Include {
		if _, err := w.Write(rsp.Header.Header()); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	if _, err := io.Copy(w, body); err != nil {
		return fmt.Errorf("copy response body: %w", err)
	}

	printTiming(ew, opts, elapsed)
	return nil
}

// bodyReader returns the response body as a reader: the stream when the
// transport left one, else the buffer. With --compressed and a gzip
// Content-Encoding, it decodes the body.
func bodyReader(opts *config.Options, rsp *fasthttp.Response) (io.Reader, error) {
	body := rsp.BodyStream()
	if body == nil {
		body = bytes.NewReader(rsp.Body())
	}
	if opts.Stealth.Compressed && bytes.EqualFold(rsp.Header.Peek("Content-Encoding"), []byte("gzip")) {
		decoded, err := transport.DecodeGzip(body)
		if err != nil {
			return nil, fmt.Errorf("decode gzip body: %w", err)
		}
		body = decoded
	}
	return body, nil
}

// writeFile copies body to the file at name.
func writeFile(name string, body io.Reader) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	_, err = io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}

// printVerbose writes the -v lines to ew.
func printVerbose(ew io.Writer, opts *config.Options, req *fasthttp.Request) {
	fmt.Fprintf(ew, "Ghost Signature: %s\n", req.Header.Peek("X-Ghola-Identity"))
	if opts.Stealth.Impersonate != "" {
		fmt.Fprintf(ew, "Profile: %s\n", opts.Stealth.Impersonate)
		fmt.Fprintln(ew, "Transport: tls-client (pure Go)")
	}
	if opts.Stealth.StealthHeaders {
		fmt.Fprintln(ew, "Stealth Headers: enabled")
	}
	if opts.ProxyCfg.Proxy != "" {
		fmt.Fprintf(ew, "Proxy: %s\n", opts.ProxyCfg.Proxy)
	}
	if opts.Stealth.CookieJar != "" {
		fmt.Fprintf(ew, "Cookie Jar: %s\n", opts.Stealth.CookieJar)
	}
}

func printTiming(ew io.Writer, opts *config.Options, elapsed time.Duration) {
	if opts.Output.Timing {
		fmt.Fprintf(ew, "Total: %s\n", elapsed.Truncate(time.Millisecond))
	}
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
