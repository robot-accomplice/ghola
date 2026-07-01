package config

import (
	"testing"
)

func TestParseFlags_BasicURL(t *testing.T) {
	opts, done, err := ParseFlags([]string{"http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Fatal("expected done=false")
	}
	if opts.URL != "http://example.com" {
		t.Errorf("URL = %q, want %q", opts.URL, "http://example.com")
	}
	if opts.Method != "GET" {
		t.Errorf("Method = %q, want GET", opts.Method)
	}
	if opts.Stealth.Agent != "ghola" {
		t.Errorf("Agent = %q, want ghola", opts.Stealth.Agent)
	}
}

func TestParseFlags_MandatoryURL(t *testing.T) {
	_, _, err := ParseFlags([]string{})
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestParseFlags_ServeMode(t *testing.T) {
	opts, done, err := ParseFlags([]string{"--serve"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Fatal("expected done=false")
	}
	if !opts.Serve {
		t.Error("Serve should be true")
	}
}

func TestParseFlags_ServeDoesNotRequireURL(t *testing.T) {
	_, _, err := ParseFlags([]string{"--serve"})
	if err != nil {
		t.Fatalf("--serve should not require a URL: %v", err)
	}
}

func TestParseFlags_Help(t *testing.T) {
	_, done, err := ParseFlags([]string{"--help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Fatal("expected done=true for --help")
	}
}

func TestParseFlags_Version(t *testing.T) {
	_, done, err := ParseFlags([]string{"--version"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Fatal("expected done=true for --version")
	}
}

func TestParseFlags_PostInference(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-d", "payload", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Method != "POST" {
		t.Errorf("Method = %q, want POST when -d is set", opts.Method)
	}
	if opts.Data != "payload" {
		t.Errorf("Data = %q, want payload", opts.Data)
	}
}

func TestParseFlags_ExplicitMethodOverridesPostInference(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-X", "PUT", "-d", "payload", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Method != "PUT" {
		t.Errorf("Method = %q, want PUT when explicitly set", opts.Method)
	}
}

func TestParseFlags_WgetFilename(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"file path", "http://example.com/archive.tar.gz", "archive.tar.gz"},
		{"root path", "http://example.com/", "index.html"},
		{"bare host", "http://example.com", "index.html"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, _, err := ParseFlags([]string{"-w", tt.url})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.Output.File != tt.want {
				t.Errorf("Output = %q, want %q", opts.Output.File, tt.want)
			}
			if !opts.Output.WgetMode {
				t.Error("WgetMode should be true")
			}
		})
	}
}

func TestParseFlags_WgetWithExplicitOutput(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-w", "-o", "custom.bin", "http://example.com/file.zip"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Output.File != "custom.bin" {
		t.Errorf("Output = %q, want custom.bin (explicit -o should override wget inference)", opts.Output.File)
	}
}

func TestParseFlags_ChainShortcuts(t *testing.T) {
	tests := []struct {
		chain      string
		wantHeader string
	}{
		{"eth", "X-Chain: ethereum"},
		{"ethereum", "X-Chain: ethereum"},
		{"base", "X-Chain: base"},
		{"solana", "X-Chain: solana"},
		{"sol", "X-Chain: solana"},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			opts, _, err := ParseFlags([]string{"-c", tt.chain, "http://example.com"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			found := false
			for _, h := range opts.Headers {
				if h == tt.wantHeader {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Headers %v does not contain %q", opts.Headers, tt.wantHeader)
			}
		})
	}
}

func TestParseFlags_UnknownChain(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-c", "unknown", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, h := range opts.Headers {
		if len(h) > 8 && h[:8] == "X-Chain:" {
			t.Errorf("unexpected chain header %q for unknown chain", h)
		}
	}
}

func TestParseFlags_AllFlags(t *testing.T) {
	args := []string{
		"-d", "body",
		"-f",
		"-i",
		"-o", "out.json",
		"-n", "4",
		"-s",
		"-T", "upload.bin",
		"-u", "admin:secret",
		"-A", "custom-agent",
		"-X", "PATCH",
		"-H", "X-Custom: val",
		"-v",
		"-D", "50",
		"-G",
		"-r", "3",
		"-b", "500",
		"-S",
		"-c", "eth",
		"--buffer-size", "8192",
		"--impersonate", "chrome",
		"--cookie-jar", "cookies.json",
		"--cookie", "session=abc",
		"--proxy", "http://proxy.local:8080",
		"--proxy-auth", "user:pass",
		"--referer", "auto",
		"--accept-language", "de-CH,de;q=0.9",
		"http://example.com",
	}
	opts, _, err := ParseFlags(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Data != "body" {
		t.Errorf("Data = %q", opts.Data)
	}
	if !opts.Output.Fail {
		t.Error("Fail should be true")
	}
	if !opts.Output.Include {
		t.Error("Include should be true")
	}
	if opts.Output.File != "out.json" {
		t.Errorf("Output = %q", opts.Output.File)
	}
	if opts.Concurrency != 4 {
		t.Errorf("Concurrency = %d", opts.Concurrency)
	}
	if !opts.Output.Silent {
		t.Error("Silent should be true")
	}
	if opts.Transfer != "upload.bin" {
		t.Errorf("Transfer = %q", opts.Transfer)
	}
	if opts.User != "admin:secret" {
		t.Errorf("User = %q", opts.User)
	}
	if opts.Stealth.Agent != "custom-agent" {
		t.Errorf("Agent = %q", opts.Stealth.Agent)
	}
	if opts.Method != "PATCH" {
		t.Errorf("Method = %q", opts.Method)
	}
	if !opts.Output.Verbose {
		t.Error("Verbose should be true")
	}
	if opts.Stealth.Drift != 50 {
		t.Errorf("Drift = %d", opts.Stealth.Drift)
	}
	if !opts.Stealth.Ghost {
		t.Error("Ghost should be true")
	}
	if opts.Resilience.Retries != 3 {
		t.Errorf("Retries = %d", opts.Resilience.Retries)
	}
	if opts.Resilience.Backoff != 500 {
		t.Errorf("Backoff = %d", opts.Resilience.Backoff)
	}
	if !opts.Output.Snoop {
		t.Error("Snoop should be true")
	}
	if opts.Resilience.BufferSize != 8192 {
		t.Errorf("BufferSize = %d, want 8192", opts.Resilience.BufferSize)
	}
	if opts.Stealth.Impersonate != "chrome" {
		t.Errorf("Impersonate = %q", opts.Stealth.Impersonate)
	}
	if !opts.Stealth.StealthHeaders {
		t.Error("StealthHeaders should default on when impersonate is set")
	}
	if opts.Stealth.CookieJar != "cookies.json" {
		t.Errorf("CookieJar = %q", opts.Stealth.CookieJar)
	}
	if len(opts.Stealth.Cookies) != 1 || opts.Stealth.Cookies[0] != "session=abc" {
		t.Errorf("Cookies = %v", opts.Stealth.Cookies)
	}
	if opts.ProxyCfg.Proxy != "http://proxy.local:8080" {
		t.Errorf("Proxy = %q", opts.ProxyCfg.Proxy)
	}
	if opts.ProxyCfg.Auth != "user:pass" {
		t.Errorf("ProxyAuth = %q", opts.ProxyCfg.Auth)
	}
	if opts.Stealth.Referer != "auto" {
		t.Errorf("Referer = %q", opts.Stealth.Referer)
	}
	if opts.Stealth.AcceptLanguage != "de-CH,de;q=0.9" {
		t.Errorf("AcceptLanguage = %q", opts.Stealth.AcceptLanguage)
	}
}

func TestParseFlags_DefaultBufferSize(t *testing.T) {
	opts, _, err := ParseFlags([]string{"http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Resilience.BufferSize != 4096 {
		t.Errorf("default BufferSize = %d, want 4096", opts.Resilience.BufferSize)
	}
}

func TestParseFlags_CustomBufferSize(t *testing.T) {
	opts, _, err := ParseFlags([]string{"--buffer-size", "16384", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Resilience.BufferSize != 16384 {
		t.Errorf("BufferSize = %d, want 16384", opts.Resilience.BufferSize)
	}
}

func TestParseFlags_InvalidFlag(t *testing.T) {
	_, _, err := ParseFlags([]string{"--nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestExitCode_Int(t *testing.T) {
	tests := []struct {
		code ExitCode
		want int
	}{
		{NoError, 0},
		{BadFlag, 1},
		{SendFailed, 2},
		{ReadFileFailed, 3},
		{WriteFileFailed, 4},
	}
	for _, tt := range tests {
		if got := tt.code.Int(); got != tt.want {
			t.Errorf("ExitCode(%d).Int() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestParseFlags_DefaultHeaders(t *testing.T) {
	opts, _, err := ParseFlags([]string{"http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, h := range opts.Headers {
		if h == "Content-Type: application/json" {
			found = true
		}
	}
	if !found {
		t.Error("default Content-Type header missing")
	}
}

func TestParseFlags_Concurrency(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-n", "8", "http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", opts.Concurrency)
	}
}

func TestParseFlags_DefaultConcurrency(t *testing.T) {
	opts, _, err := ParseFlags([]string{"http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Concurrency != 1 {
		t.Errorf("default Concurrency = %d, want 1", opts.Concurrency)
	}
}

func TestParseFlags_DefaultBackoff(t *testing.T) {
	opts, _, err := ParseFlags([]string{"http://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Resilience.Backoff != 1000 {
		t.Errorf("default Backoff = %d, want 1000", opts.Resilience.Backoff)
	}
}

func TestParseFlags_ContinueAt(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-C", "90000", "-o", "out.bin", "http://example.com/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Output.ContinueAt != "90000" {
		t.Errorf("ContinueAt = %q, want 90000", opts.Output.ContinueAt)
	}
}

func TestParseFlags_ContinueAtAuto(t *testing.T) {
	opts, _, err := ParseFlags([]string{"-C", "-", "-o", "out.bin", "http://example.com/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Output.ContinueAt != "-" {
		t.Errorf("ContinueAt = %q, want '-'", opts.Output.ContinueAt)
	}
}

func TestParseFlags_ContinueAtInvalid(t *testing.T) {
	_, _, err := ParseFlags([]string{"-C", "notanint", "-o", "out.bin", "http://example.com/file"})
	if err == nil {
		t.Fatal("expected error for non-integer --continue-at")
	}
}

func TestParseFlags_Range(t *testing.T) {
	opts, _, err := ParseFlags([]string{"--range", "0-1023", "-o", "out.bin", "http://example.com/file"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Output.Range != "0-1023" {
		t.Errorf("Range = %q, want 0-1023", opts.Output.Range)
	}
}

func TestParseFlags_ContinueAtAndRangeMutuallyExclusive(t *testing.T) {
	_, _, err := ParseFlags([]string{"-C", "-", "--range", "0-1023", "-o", "out.bin", "http://example.com/file"})
	if err == nil {
		t.Fatal("expected error for --continue-at and --range together")
	}
}

func TestShouldStream(t *testing.T) {
	cases := []struct {
		name string
		opts *Options
		want bool
	}{
		{"file get", &Options{Method: "GET", Output: OutputOptions{File: "x"}}, true},
		{"stdout", &Options{Method: "GET"}, false},
		{"jq forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", JQ: ".a"}}, false},
		{"snoop forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", Snoop: true}}, false},
		{"har forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", HAR: "h.har"}}, false},
		{"post not streamed", &Options{Method: "POST", Output: OutputOptions{File: "x"}}, false},
	}
	for _, tc := range cases {
		if got := ShouldStream(tc.opts); got != tc.want {
			t.Errorf("%s: ShouldStream = %v, want %v", tc.name, got, tc.want)
		}
	}
}
