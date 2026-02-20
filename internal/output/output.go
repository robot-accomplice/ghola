// Package output handles rendering HTTP responses to stdout or files,
// and the snoop-mode security posture report.
package output

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// ProcessResponse writes the HTTP response to the appropriate destination
// (file or stdout) based on opts. It returns a non-nil error if file I/O fails.
func ProcessResponse(w io.Writer, opts *config.Options, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpOk := rsp.StatusCode() >= 200 && rsp.StatusCode() < 300

	if opts.Fail && !httpOk {
		return nil
	}

	if opts.Output != "" {
		if err := os.WriteFile(opts.Output, rsp.Body(), 0644); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		return nil
	}

	if opts.Silent {
		return nil
	}

	if opts.Include {
		fmt.Fprintf(w, "%s\n\n", bytes.Trim(rsp.Header.Header(), "\n"))
	}
	if opts.Verbose {
		fmt.Fprintf(w, "Ghost Signature: %s\n", req.Header.Peek("X-Ghola-Identity"))
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
