// Package output handles rendering HTTP responses to stdout or files,
// and the snoop-mode security posture report.
package output

import (
	"bytes"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/transport"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// ProcessResponse writes the HTTP response to the appropriate destination
// (file or stdout) based on opts. The body goes to w exactly as the server
// sent it, without a buffer (audit findings H2 and M2). -i puts the header
// block on w in front of the body, as curl -i does. Metadata (-v lines and
// the timing line) goes to ew. It returns a non-nil error if the body cannot
// be read or written.
func ProcessResponse(w, ew io.Writer, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response, elapsed time.Duration) error {
	if opts.Output.TraceTime {
		ew = timedLines{ew} // --trace-time also times the -v lines
	}
	failErr := statusError(opts, rsp.StatusCode())
	// -o writes no file for a refused status. stdout keeps the body (v1
	// rule, as curl --fail-with-body); the v0 rule drops it.
	if failErr != nil && (opts.Output.File != "" || !opts.Output.FailFrom400) {
		return failErr
	}

	if err := opts.Output.SaveETag(rsp.StatusCode(), string(rsp.Header.Peek("ETag"))); err != nil {
		return err
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
		if opts.Output.CondTime && rsp.StatusCode() == fasthttp.StatusNotModified {
			printTiming(ew, opts, elapsed)
			return nil // the condition is not met: no file, as curl -z
		}
		if err := writeFile(opts.Output, opts.Output.File, body); err != nil {
			return err
		}
		printTiming(ew, opts, elapsed)
		return nil
	}

	if opts.Output.Verbose {
		printVerbose(ew, opts, req)
	}
	if opts.Output.Include {
		if _, err := w.Write(rsp.Header.Header()); err != nil {
			return fmt.Errorf("write output: %w", config.WriteError(err))
		}
	}
	digest := opts.Output.DigestFor(rsp.StatusCode(), string(rsp.Header.Peek("Content-Digest")), string(rsp.Header.Peek("Content-Encoding")))
	var sum hash.Hash
	if digest != nil {
		sum = digest.NewHash()
		body = io.TeeReader(body, sum)
	}
	n, err := io.Copy(config.MarkWrites(bodyOut(w, opts)), body)
	opts.Output.Info.AddSize(n)
	if err != nil {
		return fmt.Errorf("copy response body: %w", err)
	}
	if digest != nil {
		if err := digest.Check(sum.Sum(nil), "the body"); err != nil {
			return err
		}
	}

	printTiming(ew, opts, elapsed)
	return failErr
}

// statusError returns the error of a status that --fail refuses, else nil.
func statusError(opts *config.Options, status int) error {
	if !opts.Output.FailsOn(status) {
		return nil
	}
	return fmt.Errorf("%w: %d %s", config.ErrHTTPStatus, status, fasthttp.StatusMessage(status))
}

// bodyReader returns the response body as a reader: the stream when the
// transport left one, else the buffer. With --compressed and a gzip
// Content-Encoding, it decodes the body. With --limit-rate, it throttles it.
func bodyReader(opts *config.Options, rsp *fasthttp.Response) (io.Reader, error) {
	body := rsp.BodyStream()
	if body == nil {
		body = bytes.NewReader(rsp.Body())
	}
	// The limit counts the bytes on the wire, as in curl, so it comes
	// before the gzip decoder.
	body, err := client.LimitReader(body, opts)
	if err != nil {
		return nil, err
	}
	if opts.Output.TooLarge(int64(rsp.Header.ContentLength())) {
		return nil, config.ErrTooLarge
	}
	if opts.Stealth.Compressed {
		if body, err = decodedBody(body, string(rsp.Header.Peek("Content-Encoding"))); err != nil {
			return nil, err
		}
	}
	// --max-size counts the bytes that ghola writes, after the decoder, as
	// the download path does. So a small compressed body that decodes to
	// gigabytes stops at the limit.
	return opts.Output.LimitReader(body), nil
}

// decodedBody removes the content codings from body. A body that does not
// decode is a bad content encoding (curl exit 61), not a receive failure:
// a read error that does not come from the network is the decoder's.
func decodedBody(body io.Reader, contentEncoding string) (io.Reader, error) {
	wire := &readSpy{r: body}
	decoded, err := transport.DecodeBody(wire, contentEncoding)
	if err != nil {
		return nil, badEncoding(wire, err)
	}
	return decodeErrors{decoded, wire}, nil
}

// readSpy keeps the last error of the network reader.
type readSpy struct {
	r   io.Reader
	err error
}

func (s *readSpy) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.err = err
	}
	return n, err
}

// decodeErrors marks the read errors of the decoder.
type decodeErrors struct {
	r    io.Reader
	wire *readSpy
}

func (d decodeErrors) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil && err != io.EOF {
		err = badEncoding(d.wire, err)
	}
	return n, err
}

// badEncoding marks err as a bad content encoding, unless the network
// reader failed.
func badEncoding(wire *readSpy, err error) error {
	if wire.err != nil || errors.Is(err, transport.ErrBadEncoding) {
		return err
	}
	return fmt.Errorf("%w: %w", transport.ErrBadEncoding, err)
}

// writeFile copies body to the file at name.
func writeFile(out config.OutputOptions, name string, body io.Reader) error {
	f, err := config.OpenOutput(out, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false)
	if err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	n, err := io.Copy(config.MarkWrites(f), body)
	out.Info.AddSize(n)
	if cerr := f.Close(); err == nil {
		err = config.WriteError(cerr)
	}
	if err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}

// printVerbose writes the -v lines to ew.
func printVerbose(ew io.Writer, opts *config.Options, req *fasthttp.Request) {
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

// timedLines puts the time in front of each -v line. Each line is one
// write.
type timedLines struct{ w io.Writer }

func (t timedLines) Write(p []byte) (int, error) {
	if _, err := io.WriteString(t.w, time.Now().Format("15:04:05.000000 ")); err != nil {
		return 0, err
	}
	return t.w.Write(p)
}

func printTiming(ew io.Writer, opts *config.Options, elapsed time.Duration) {
	if opts.Output.Verbose { // -v writes the total time (v0 --timing)
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
