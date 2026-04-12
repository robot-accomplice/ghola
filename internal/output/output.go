// Package output handles rendering HTTP responses to stdout or files,
// and the snoop-mode security posture report.
package output

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// ErrNon2xx is returned when --fail is set and the server responds with a non-2xx status.
var ErrNon2xx = errors.New("non-2xx http status")

// ProcessResponse writes the HTTP response to the appropriate destination
// (file or stdout) based on opts. It returns a non-nil error if file I/O fails.
func ProcessResponse(w io.Writer, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpOk := rsp.StatusCode() >= 200 && rsp.StatusCode() < 300

	if opts.Output.Fail && !httpOk {
		return fmt.Errorf("%w: %d %s", ErrNon2xx, rsp.StatusCode(), fasthttp.StatusMessage(rsp.StatusCode()))
	}

	if opts.Output.File != "" {
		if err := os.WriteFile(opts.Output.File, rsp.Body(), 0644); err != nil {
			return fmt.Errorf("write output file: %w", err)
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
	fmt.Fprintf(w, "%s\n", bytes.Trim(rsp.Body(), "\n"))
	return nil
}

// RunSnoop prints a security posture report for the target URL.
func RunSnoop(w io.Writer, opts *config.Options, rsp *fasthttp.Response) {
	fmt.Fprintln(w, "--- [Ghola Snoop Mode] ---")
	fmt.Fprintf(w, "Target: %s\n", opts.URL)
	fmt.Fprintf(w, "Status: %d %s\n", rsp.StatusCode(), fasthttp.StatusMessage(rsp.StatusCode()))
	fmt.Fprintf(w, "Content Length: %d bytes\n", rsp.Header.ContentLength())
	fmt.Fprintf(w, "Server: %s\n", rsp.Header.Peek("Server"))
	fmt.Fprintf(w, "WAF/Security: ")
	if bytes.Contains(rsp.Header.Peek("Server"), []byte("cloudflare")) ||
		bytes.Contains(rsp.Header.Peek("X-Ray"), []byte("true")) {
		fmt.Fprintln(w, "DETECTED (Shielding present)")
	} else {
		fmt.Fprintln(w, "LOW (Minimal visible shielding)")
	}
	fmt.Fprintln(w, "--- [Snoop End] ---")
}
