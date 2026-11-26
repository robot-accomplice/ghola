package client

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// A permanent error gets the same answer on each attempt, so a retry only
// costs time; for a refused login it sends the password again, and a
// server can lock the account. ghola retried each one 3 times.
func TestFetchDoesNotRetryAPermanentError(t *testing.T) {
	cases := map[string]error{
		"untrusted cert":   fmt.Errorf("x: %w", x509.UnknownAuthorityError{}),
		"refused login":    fmt.Errorf("ftp: %w", gholatransport.ErrLoginDenied),
		"missing file":     fmt.Errorf("x: %w", gholatransport.ErrRemoteNotFound),
		"file:// not read": fmt.Errorf("x: %w", gholatransport.ErrFileRead),
		"SSH host key":     fmt.Errorf("x: %w", gholatransport.ErrSSHHostKey),
		"pin mismatch":     fmt.Errorf("x: %w", gholatransport.ErrPinMismatch),
		"revoked cert":     fmt.Errorf("x: %w", gholatransport.ErrCertRevoked),
		"no OCSP staple":   fmt.Errorf("x: %w", gholatransport.ErrCertStatus),
		"bad encoding":     fmt.Errorf("x: %w", gholatransport.ErrBadEncoding),
		"invalid on stack": fmt.Errorf("x: %w", ErrInvalidRequest),
	}
	for name, cause := range cases {
		calls := 0
		do := func(context.Context, *config.Options, *fasthttp.Request, *fasthttp.Response) error {
			calls++
			return cause
		}
		opts := &config.Options{URL: "http://a.test/", Method: "GET",
			Output: config.OutputOptions{Silent: true}, Resilience: config.ResilienceOptions{Retries: 3, Backoff: 1}}
		_, _, err := FetchURL(bg(), opts, do)
		if !errors.Is(err, cause) || calls != 1 {
			t.Errorf("%s: %d attempts, err %v; want 1 attempt and the cause", name, calls, err)
		}
	}
}

// A refused connection or a reset can pass, so it is still retried.
func TestFetchRetriesATransientError(t *testing.T) {
	calls := 0
	do := func(_ context.Context, _ *config.Options, _ *fasthttp.Request, rsp *fasthttp.Response) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("%w: connection reset", gholatransport.ErrRecv)
		}
		rsp.SetStatusCode(200)
		return nil
	}
	opts := &config.Options{URL: "http://a.test/", Method: "GET",
		Output: config.OutputOptions{Silent: true}, Resilience: config.ResilienceOptions{Retries: 3, Backoff: 1}}
	req, rsp, err := FetchURL(bg(), opts, do)
	if err != nil || calls != 3 {
		t.Fatalf("%d attempts, err %v; want the third attempt to answer", calls, err)
	}
	Release(req, rsp)
}

// A request value that cannot go on the wire exits 2 before the first
// attempt, and a line break in a header is refused, not changed to a
// space. ghola sent the request 4 times and exited 55 or 56.
func TestFetchRefusesAnInvalidRequestValueAtOnce(t *testing.T) {
	cases := map[string]config.Options{
		"method with a space":   {Method: "GE T"},
		"header name with ':'":  {Method: "GET", Headers: []string{"X(y: 1"}},
		"line break in a value": {Method: "GET", Headers: []string{"X-A: 1\r\nX-B: 2"}},
		"line break in the UA":  {Method: "GET", Stealth: config.StealthOptions{Agent: "a\nb"}},
		"space in the URL":      {Method: "GET", URL: "http://a.test/a b"},
		"http 3 on the default": {Method: "GET", Stealth: config.StealthOptions{HTTPVersion: "3"}},
	}
	for name, opts := range cases {
		if opts.URL == "" {
			opts.URL = "http://a.test/"
		}
		opts.Resilience.Retries = 3
		calls := 0
		do := func(context.Context, *config.Options, *fasthttp.Request, *fasthttp.Response) error {
			calls++
			return nil
		}
		_, _, err := FetchURL(bg(), &opts, do)
		if !errors.Is(err, ErrInvalidRequest) || calls != 0 || ExitCodeFor(err) != config.BadFlag {
			t.Errorf("%s: %d attempts, err %v; want exit 2 before any attempt", name, calls, err)
		}
	}
}
