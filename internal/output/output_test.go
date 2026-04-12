package output

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

func TestProcessResponse_Stdout(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("hello world")

	var buf bytes.Buffer
	opts := &config.Options{}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "hello world") {
		t.Errorf("output = %q, want to contain 'hello world'", buf.String())
	}
}

func TestProcessResponse_IncludeHeaders(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("X-Test", "headerval")
	rsp.SetBodyString("body")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Include: true}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "X-Test: headerval") {
		t.Errorf("output missing header, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "body") {
		t.Errorf("output missing body, got %q", buf.String())
	}
}

func TestProcessResponse_Verbose(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	req.Header.Set("X-Ghola-Identity", "abc123")
	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("data")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Verbose: true}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Ghost Signature: abc123") {
		t.Errorf("verbose output missing ghost signature, got %q", buf.String())
	}
}

func TestProcessResponse_Silent(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("should not appear")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Silent: true}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("silent mode should produce no output, got %q", buf.String())
	}
}

func TestProcessResponse_FailOnHTTPError(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(500)
	rsp.SetBodyString("error body")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Fail: true}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err == nil {
		t.Fatal("expected error for non-2xx with fail=true")
	}
	if buf.Len() != 0 {
		t.Errorf("fail mode with 500 should suppress output, got %q", buf.String())
	}
}

func TestProcessResponse_FailWithSuccess(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("ok")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Fail: true}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "ok") {
		t.Errorf("fail mode with 200 should still output, got %q", buf.String())
	}
}

func TestProcessResponse_FileOutput(t *testing.T) {
	tmpFile := t.TempDir() + "/output.txt"

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("file content")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{File: tmpFile}}
	err := ProcessResponse(&buf, opts, req, rsp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != "file content" {
		t.Errorf("file content = %q, want 'file content'", content)
	}

	info, _ := os.Stat(tmpFile)
	if info.Mode().Perm() != 0644 {
		t.Errorf("file permissions = %o, want 0644", info.Mode().Perm())
	}

	if buf.Len() != 0 {
		t.Errorf("file output mode should not write to stdout, got %q", buf.String())
	}
}

func TestProcessResponse_FileOutputFailSuppresses(t *testing.T) {
	tmpFile := t.TempDir() + "/output.txt"

	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(404)
	rsp.SetBodyString("not found")

	opts := &config.Options{Output: config.OutputOptions{File: tmpFile, Fail: true}}
	var buf bytes.Buffer
	err := ProcessResponse(&buf, opts, req, rsp)
	if err == nil {
		t.Fatal("expected error for non-2xx with fail=true")
	}

	if _, statErr := os.Stat(tmpFile); statErr == nil {
		t.Error("file should not be written when fail=true and status is non-2xx")
	}
}

func TestProcessResponse_FileOutputError(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.SetBodyString("data")

	opts := &config.Options{Output: config.OutputOptions{File: "/nonexistent/dir/file.txt"}}
	var buf bytes.Buffer
	err := ProcessResponse(&buf, opts, req, rsp)
	if err == nil {
		t.Fatal("expected error for invalid output path")
	}
}

func TestRunSnoop_CloudflareDetected(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(403)
	rsp.Header.Set("Server", "cloudflare")
	rsp.Header.SetContentLength(1234)

	var buf bytes.Buffer
	opts := &config.Options{URL: "http://target.com"}
	RunSnoop(&buf, opts, rsp)

	out := buf.String()
	if !strings.Contains(out, "--- [Ghola Snoop Mode] ---") {
		t.Error("missing snoop header")
	}
	if !strings.Contains(out, "http://target.com") {
		t.Error("missing target URL")
	}
	if !strings.Contains(out, "403") {
		t.Error("missing status code")
	}
	if !strings.Contains(out, "DETECTED") {
		t.Error("should detect cloudflare WAF")
	}
	if !strings.Contains(out, "--- [Snoop End] ---") {
		t.Error("missing snoop footer")
	}
}

func TestRunSnoop_NoWAF(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("Server", "nginx")

	var buf bytes.Buffer
	opts := &config.Options{URL: "http://clean.com"}
	RunSnoop(&buf, opts, rsp)

	out := buf.String()
	if !strings.Contains(out, "LOW") {
		t.Error("should report LOW for non-cloudflare server")
	}
}

func TestRunSnoop_XRayDetected(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("Server", "nginx")
	rsp.Header.Set("X-Ray", "true")

	var buf bytes.Buffer
	opts := &config.Options{URL: "http://xray.com"}
	RunSnoop(&buf, opts, rsp)

	if !strings.Contains(buf.String(), "DETECTED") {
		t.Error("should detect WAF via X-Ray header")
	}
}

func TestProcessResponse_StatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		fail   bool
		output bool
	}{
		{"200 no fail", 200, false, true},
		{"200 with fail", 200, true, true},
		{"301 no fail", 301, false, true},
		{"404 no fail", 404, false, true},
		{"404 with fail", 404, true, false},
		{"500 with fail", 500, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := fasthttp.AcquireRequest()
			rsp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(req)
			defer fasthttp.ReleaseResponse(rsp)

			rsp.Header.SetStatusCode(tt.status)
			rsp.SetBodyString("content")

			var buf bytes.Buffer
			opts := &config.Options{Output: config.OutputOptions{Fail: tt.fail}}
			_ = ProcessResponse(&buf, opts, req, rsp)

			hasOutput := buf.Len() > 0
			if hasOutput != tt.output {
				t.Errorf("output=%v, want %v (status=%d, fail=%v)", hasOutput, tt.output, tt.status, tt.fail)
			}
		})
	}
}

func TestRunSnoop_ContentLength(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.SetContentLength(42)
	rsp.Header.Set("Server", "apache")

	var buf bytes.Buffer
	opts := &config.Options{URL: "http://example.com"}
	RunSnoop(&buf, opts, rsp)

	if !strings.Contains(buf.String(), "42 bytes") {
		t.Error("snoop should display content length")
	}
}
