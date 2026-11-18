//go:build parity

package parity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Env gives a scenario the URLs and reference content for one run.
type Env struct {
	s   *server
	key string
}

// URL is a plain-HTTP URL for path inside this run's prefix.
func (e *Env) URL(path string) string { return e.s.plain.URL + "/s/" + e.key + path }

// TLSURL is the same path on the TLS server. Its certificate is self-signed.
func (e *Env) TLSURL(path string) string { return e.s.tls.URL + "/s/" + e.key + path }

// Ref is the content that /file/<size> serves.
func (e *Env) Ref(size string) []byte { return e.s.content(parseSize(size)) }

func (e *Env) CAFile() string  { return e.s.caFile }
func (e *Env) DeadURL() string { return e.s.deadURL }

// SilentURL is an https URL whose server accepts TCP and never answers.
func (e *Env) SilentURL() string { return e.s.silentURL }

// check returns nil when a run passes, or the reason it fails.
type check func(r *Result, e *Env) error

func all(cs ...check) check {
	return func(r *Result, e *Env) error {
		for _, c := range cs {
			if err := c(r, e); err != nil {
				return err
			}
		}
		return nil
	}
}

// either passes when one of the checks passes.
func either(cs ...check) check {
	return func(r *Result, e *Env) error {
		var errs []string
		for _, c := range cs {
			err := c(r, e)
			if err == nil {
				return nil
			}
			errs = append(errs, err.Error())
		}
		return errors.New(strings.Join(errs, "; and "))
	}
}

func exitZero(r *Result, _ *Env) error {
	if r.TimedOut {
		return fmt.Errorf("killed after %s", runTimeout)
	}
	if r.Exit != 0 {
		return fmt.Errorf("exit %d, want 0", r.Exit)
	}
	return nil
}

func exitNonzero(r *Result, _ *Env) error {
	if r.TimedOut {
		return fmt.Errorf("killed after %s", runTimeout)
	}
	if r.Exit == 0 {
		return errors.New("exit 0, want an error exit")
	}
	return nil
}

func exitIs(code int) check {
	return func(r *Result, _ *Env) error {
		if r.TimedOut || r.Exit != code {
			return fmt.Errorf("exit %d (timed out %v), want %d", r.Exit, r.TimedOut, code)
		}
		return nil
	}
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// stdoutIs checks that stdout is byte-exact.
func stdoutIs(want func(e *Env) []byte) check {
	return func(r *Result, e *Env) error {
		w := want(e)
		if r.StdoutSHA != sha(w) {
			return fmt.Errorf("stdout has %d bytes and differs from the %d-byte body%s", r.StdoutLen, len(w), diffHint(r.stdout, w))
		}
		return nil
	}
}

// fileIs checks that the file name in the working directory is byte-exact.
func fileIs(name string, want func(e *Env) []byte) check {
	return func(r *Result, e *Env) error {
		got, ok := r.file(name)
		if !ok {
			return fmt.Errorf("file %s is absent", name)
		}
		w := want(e)
		if !bytes.Equal(got, w) {
			return fmt.Errorf("file %s has %d bytes and differs from the %d-byte body%s", name, len(got), len(w), diffHint(got, w))
		}
		return nil
	}
}

func fileAbsent(name string) check {
	return func(r *Result, _ *Env) error {
		if b, ok := r.file(name); ok {
			return fmt.Errorf("file %s exists (%d bytes), want no file", name, len(b))
		}
		return nil
	}
}

// fileIsSeed checks that the partial file from Spec.Seed is unchanged.
func fileIsSeed(r *Result, _ *Env) error {
	got, ok := r.file("f")
	if !ok {
		return errors.New("partial file f was removed")
	}
	if !bytes.Equal(got, r.seed) {
		return fmt.Errorf("partial file f changed from %d to %d bytes", len(r.seed), len(got))
	}
	return nil
}

func diffHint(got, want []byte) string {
	if got == nil {
		return ""
	}
	for i := 0; i < min(len(got), len(want)); i++ {
		if got[i] != want[i] {
			return fmt.Sprintf(" (first difference at byte %d)", i)
		}
	}
	return ""
}

func fasterThan(ms int64) check {
	return func(r *Result, _ *Env) error {
		if r.TimedOut || r.ElapsedMS >= ms {
			return fmt.Errorf("took %d ms, want under %d ms", r.ElapsedMS, ms)
		}
		return nil
	}
}

func slowerThan(ms int64) check {
	return func(r *Result, _ *Env) error {
		if r.ElapsedMS < ms {
			return fmt.Errorf("took %d ms, want at least %d ms (rate limit not applied)", r.ElapsedMS, ms)
		}
		return nil
	}
}

func peakBelow(limit int64) check {
	return func(r *Result, _ *Env) error {
		if r.PeakRSS == 0 {
			return errors.New("peak memory was not measured")
		}
		if r.PeakRSS >= limit {
			return fmt.Errorf("peak memory %d MiB, want under %d MiB", r.PeakRSS>>20, limit>>20)
		}
		return nil
	}
}

func countRequests(r *Result, method string) int {
	n := 0
	for _, q := range r.Requests {
		if q.Method == method {
			n++
		}
	}
	return n
}

func requestsAre(method string, want int) check {
	return func(r *Result, _ *Env) error {
		if n := countRequests(r, method); n != want {
			return fmt.Errorf("server got %d %s requests, want %d", n, method, want)
		}
		return nil
	}
}

func noRequests(r *Result, _ *Env) error {
	if len(r.Requests) > 0 {
		return fmt.Errorf("server got %d requests, want none (bad input must stop before the network)", len(r.Requests))
	}
	return nil
}

// lastTo checks the last request to path. hostPrefix limits the match to one
// host name ("" matches any).
func lastTo(path, hostPrefix string, ok func(q reqLog) error) check {
	return func(r *Result, _ *Env) error {
		for i := len(r.Requests) - 1; i >= 0; i-- {
			q := r.Requests[i]
			if q.Path == path && strings.HasPrefix(q.Host, hostPrefix) {
				return ok(q)
			}
		}
		return fmt.Errorf("no request reached %s on host %q*", path, hostPrefix)
	}
}

func headerAbsent(name string) func(reqLog) error {
	return func(q reqLog) error {
		if v := q.Header.Values(name); len(v) > 0 {
			return fmt.Errorf("%s %s on %s carried %s: %q", q.Method, q.Path, q.Host, name, v)
		}
		return nil
	}
}

func headerIs(name, want string) func(reqLog) error {
	return func(q reqLog) error {
		if got := q.Header.Get(name); got != want {
			return fmt.Errorf("%s = %q, want %q", name, got, want)
		}
		return nil
	}
}

func methodBody(method, body string) func(reqLog) error {
	return func(q reqLog) error {
		if q.Method != method || q.Body != body {
			return fmt.Errorf("got %s with body %q, want %s with body %q", q.Method, q.Body, method, body)
		}
		return nil
	}
}

func stderrLacks(subs ...string) check {
	return func(r *Result, _ *Env) error {
		for _, s := range subs {
			if strings.Contains(r.Stderr, s) {
				return fmt.Errorf("stderr contains %q", s)
			}
		}
		return nil
	}
}

func stderrNotEmpty(r *Result, _ *Env) error {
	if strings.TrimSpace(r.Stderr) == "" {
		return errors.New("stderr is empty, want a message")
	}
	return nil
}

var percent = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)

// meterSigns are the marks of a progress meter: a percentage (ghola, aria2),
// curl's table header, or a carriage return that redraws a line. A CR before
// LF only ends a line (Wget2 on Windows writes CRLF), so it does not count.
var meterSigns = regexp.MustCompile(`\d+(?:\.\d+)?%|% Total|\r[^\n]`)

// noMeter fails when the console output has a progress meter. The body goes
// to a file in the scenarios that use it, so stdout is console output too
// (aria2 writes its console output to stdout).
func noMeter(r *Result, _ *Env) error {
	for name, s := range map[string]string{"stderr": r.Stderr, "stdout": string(r.stdout)} {
		if m := meterSigns.FindString(s); m != "" {
			return fmt.Errorf("%s is not a terminal, but has progress output (%q)", name, m)
		}
	}
	return nil
}

// percentAtMost100 fails when a progress meter shows more than 100%.
func percentAtMost100(r *Result, _ *Env) error {
	for _, m := range percent.FindAllStringSubmatch(r.Stderr, -1) {
		if v, _ := strconv.ParseFloat(m[1], 64); v > 100 {
			return fmt.Errorf("progress shows %s", m[0])
		}
	}
	return nil
}

// noFileNamed fails when name exists anywhere in the work tree outside the
// tool's working directory: a path traversal.
func noEscape(name string) check {
	return func(r *Result, _ *Env) error {
		for p := range r.Files {
			if strings.HasSuffix(p, name) && !strings.HasPrefix(p, "a/b/") {
				return fmt.Errorf("file written outside the working directory: %s", p)
			}
		}
		return nil
	}
}

// resumeOK passes when the file is complete and correct, or when the tool
// failed and left the partial file unchanged.
func resumeOK(size string) check {
	return either(
		all(exitZero, fileIs("f", func(e *Env) []byte { return e.Ref(size) })),
		all(exitNonzero, fileIsSeed),
	)
}

// changedOK passes when the file is one whole version of /changing, or when
// the tool failed and left the partial file unchanged. A mix of the old
// partial file and a new version fails.
func changedOK(size string) check {
	return either(
		func(r *Result, e *Env) error {
			got, _ := r.file("f")
			for n := 1; n <= len(r.Requests); n++ {
				if bytes.Equal(got, changingVersion(e.s, parseSize(size), n)) {
					return nil
				}
			}
			return fmt.Errorf("file f (%d bytes) is not one whole version of the remote file", len(got))
		},
		all(exitNonzero, fileIsSeed),
	)
}

// segmentedOK passes when the file is correct, or when the tool failed and
// left no file that a later resume could take for a finished download.
func segmentedOK(size string) check {
	return either(
		all(exitZero, fileIs("f", func(e *Env) []byte { return e.Ref(size) })),
		all(exitNonzero, fileAbsent("f")),
	)
}

func ref(size string) func(e *Env) []byte { return func(e *Env) []byte { return e.Ref(size) } }
func text(s string) func(*Env) []byte     { return func(*Env) []byte { return []byte(s) } }
