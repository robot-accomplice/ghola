package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	err := ProcessResponse(&buf, opts, req, rsp, 0)
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
	if !strings.Contains(out, "not detected") {
		t.Error("should report 'not detected' for non-cloudflare server")
	}
}

func TestRunSnoop_AWSDetected(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("Server", "nginx")
	rsp.Header.Set("X-Amzn-Requestid", "abc-123")

	var buf bytes.Buffer
	opts := &config.Options{URL: "http://aws.com"}
	RunSnoop(&buf, opts, rsp)

	if !strings.Contains(buf.String(), "DETECTED") {
		t.Error("should detect AWS via X-Amzn-Requestid header")
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
			_ = ProcessResponse(&buf, opts, req, rsp, 0)

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

func TestProcessResponse_JQ(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.SetStatusCode(200)
	rsp.SetBodyString(`{"result":"0x1a2b","id":1}`)

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{JQ: "result"}}
	err := ProcessResponse(&buf, opts, req, rsp, 0)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}
	if !strings.Contains(buf.String(), "0x1a2b") {
		t.Errorf("JQ output = %q, want to contain '0x1a2b'", buf.String())
	}
}

func TestProcessResponse_JQ_NoMatch(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.SetStatusCode(200)
	rsp.SetBodyString(`{"result":"0x1a2b"}`)

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{JQ: "nonexistent"}}
	err := ProcessResponse(&buf, opts, req, rsp, 0)
	if err == nil {
		t.Fatal("expected error for non-matching JQ path")
	}
}

func TestProcessResponse_Timing(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.SetStatusCode(200)
	rsp.SetBodyString("ok")

	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{Timing: true}}
	err := ProcessResponse(&buf, opts, req, rsp, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}
	if !strings.Contains(buf.String(), "Total: 150ms") {
		t.Errorf("timing output = %q, want 'Total: 150ms'", buf.String())
	}
}

func TestProcessResponse_JQ_WithFileOutput(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	rsp.SetStatusCode(200)
	rsp.SetBodyString(`{"data":{"value":42}}`)

	tmpFile := filepath.Join(t.TempDir(), "jq-out.txt")
	var buf bytes.Buffer
	opts := &config.Options{Output: config.OutputOptions{File: tmpFile, JQ: "data.value"}}
	err := ProcessResponse(&buf, opts, req, rsp, 0)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "42" {
		t.Errorf("file content = %q, want '42'", data)
	}
}

func TestWriteHAR(t *testing.T) {
	req := fasthttp.AcquireRequest()
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	req.SetRequestURI("http://example.com/test?q=1")
	req.Header.SetMethod("GET")
	rsp.SetStatusCode(200)
	rsp.Header.SetContentType("application/json")
	rsp.SetBodyString(`{"ok":true}`)

	harFile := filepath.Join(t.TempDir(), "test.har")
	start := time.Now()
	err := WriteHAR(harFile, &config.Options{}, req, rsp, start, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("WriteHAR: %v", err)
	}

	data, _ := os.ReadFile(harFile)
	if !strings.Contains(string(data), `"version": "1.2"`) {
		t.Error("HAR file missing version")
	}
	if !strings.Contains(string(data), "example.com") {
		t.Error("HAR file missing URL")
	}
	if !strings.Contains(string(data), "ok") {
		t.Errorf("HAR file missing response body, got: %s", string(data)[:min(len(data), 500)])
	}
}

func TestRunSnoop_SecurityHeaders(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("Strict-Transport-Security", "max-age=31536000")
	rsp.Header.Set("X-Frame-Options", "DENY")
	rsp.Header.Set("Content-Security-Policy", "default-src 'self'")

	var buf bytes.Buffer
	opts := &config.Options{URL: "https://secure.com"}
	RunSnoop(&buf, opts, rsp)

	out := buf.String()
	if !strings.Contains(out, "max-age=31536000") {
		t.Error("missing HSTS in snoop output")
	}
	if !strings.Contains(out, "DENY") {
		t.Error("missing X-Frame-Options in snoop output")
	}
}

func TestRunSnoop_RateLimitHeaders(t *testing.T) {
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.SetStatusCode(200)
	rsp.Header.Set("X-RateLimit-Limit", "100")
	rsp.Header.Set("X-RateLimit-Remaining", "42")

	var buf bytes.Buffer
	opts := &config.Options{URL: "https://api.com"}
	RunSnoop(&buf, opts, rsp)

	out := buf.String()
	if !strings.Contains(out, "Rate Limiting") {
		t.Error("missing rate limit section in snoop output")
	}
}
