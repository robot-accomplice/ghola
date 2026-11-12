package config

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout returns what fn writes to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	return <-done
}

// Every argument error must carry the full usage text. The CLI prints that
// text around the error, so a user who mistypes a command sees at once how
// to type it correctly. A ParseFlags error without usage would show the user
// only the bare message.
func TestParseFlags_ArgumentErrorsCarryUsage(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"missing URL", []string{}, "URL is mandatory"},
		{"unknown flag", []string{"--nope", "http://example.com"}, "unknown flag: --nope"},
		{"conflicting flags", []string{"--proxy", "http://p", "--proxy-file", "x", "http://example.com"}, "--proxy and --proxy-file cannot be used together"},
		{"unknown profile", []string{"--impersonate", "netscape", "http://example.com"}, "netscape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseFlags(tc.args)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v (%T), want *UsageError", err, err)
			}
			if !strings.Contains(ue.Error(), tc.wantMsg) {
				t.Errorf("Error() = %q, want it to contain %q", ue.Error(), tc.wantMsg)
			}
			for _, want := range []string{"Usage: ghola [options...] <url>", "--impersonate"} {
				if !strings.Contains(ue.Usage, want) {
					t.Errorf("Usage is missing %q", want)
				}
			}
		})
	}
}

// The usage text printed for an error must be the same text --help prints,
// so the two can never drift apart.
func TestUsageError_UsageMatchesHelp(t *testing.T) {
	_, _, err := ParseFlags([]string{})
	var ue *UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *UsageError", err)
	}
	help := captureStdout(t, func() {
		if _, done, err := ParseFlags([]string{"--help"}); err != nil || !done {
			t.Fatalf("--help: done=%v err=%v", done, err)
		}
	})
	if help != ue.Usage {
		t.Errorf("--help output and UsageError.Usage differ\n--help:\n%s\nUsage:\n%s", help, ue.Usage)
	}
}
