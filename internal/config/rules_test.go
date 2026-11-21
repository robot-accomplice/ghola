package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --checksum takes aria2's ALGO=HEX. A wrong length or an unknown
// algorithm is refused at once, so a typo does not let every body pass.
func TestParseChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte("body"))
	d, err := ParseChecksum("SHA-256=" + hex.EncodeToString(sum[:]))
	if err != nil || d.Algo != "sha-256" || !bytes.Equal(d.Want, sum[:]) {
		t.Fatalf("%+v %v", d, err)
	}
	for _, bad := range []string{"sha-256=abcd", "crc32=00000000", "sha-256", "md5=zz"} {
		if _, err := ParseChecksum(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// A body that does not match fails with ErrChecksum (exit 32), and a file
// is hashed as a whole.
func TestDigest_CheckAndCheckFile(t *testing.T) {
	sum := sha256.Sum256([]byte("body"))
	d := &Digest{Algo: "sha-256", Want: sum[:]}
	h := d.NewHash()
	h.Write([]byte("body")) //nolint:errcheck
	if err := d.Check(h.Sum(nil), "body"); err != nil {
		t.Error(err)
	}
	if err := d.Check([]byte("other"), "body"); !errors.Is(err, ErrChecksum) {
		t.Errorf("a wrong sum: %v", err)
	}
	path := filepath.Join(t.TempDir(), "f")
	os.WriteFile(path, []byte("body"), 0o600) //nolint:errcheck
	if err := d.CheckFile(path); err != nil {
		t.Error(err)
	}
	os.WriteFile(path, []byte("bodY"), 0o600) //nolint:errcheck
	if err := d.CheckFile(path); !errors.Is(err, ErrChecksum) {
		t.Errorf("a changed file: %v", err)
	}
	if err := d.CheckFile(filepath.Join(t.TempDir(), "none")); err == nil || errors.Is(err, ErrChecksum) {
		t.Errorf("a missing file must be a read error, not a mismatch: %v", err)
	}
}

// Content-Digest counts only for a whole 200 answer that ghola does not
// decode: it covers the bytes as sent, so a 206 or a gzip body would fail
// a good download. --checksum always wins.
func TestDigestFor(t *testing.T) {
	sum := sha256.Sum256([]byte("body"))
	header := "unknown=:AA==:, sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
	o := OutputOptions{}
	if d := o.DigestFor(200, header, ""); d == nil || d.Algo != "sha-256" || !bytes.Equal(d.Want, sum[:]) {
		t.Errorf("200: %+v", d)
	}
	for _, c := range []struct {
		status   int
		encoding string
	}{{206, ""}, {200, "gzip"}} {
		if d := o.DigestFor(c.status, header, c.encoding); d != nil {
			t.Errorf("%d %q: %+v, want none", c.status, c.encoding, d)
		}
	}
	if d := o.DigestFor(200, header, "identity"); d == nil {
		t.Error("identity is not a coding")
	}
	o.Checksum = &Digest{Algo: "md5"}
	if d := o.DigestFor(206, header, "gzip"); d != o.Checksum {
		t.Error("--checksum must win")
	}
	if ContentDigest("sha-256=:not base64!:") != nil {
		t.Error("a bad value was read")
	}
}

// v1 --fail refuses 400 and above, as curl; the v0 rule refused all but
// 2xx. Without --fail nothing is refused.
func TestFailsOn(t *testing.T) {
	v1 := OutputOptions{Fail: true, FailFrom400: true}
	v0 := OutputOptions{Fail: true}
	for status, want := range map[int][2]bool{200: {false, false}, 304: {false, true}, 404: {true, true}, 500: {true, true}} {
		if got := [2]bool{v1.FailsOn(status), v0.FailsOn(status)}; got != want {
			t.Errorf("%d: v1, v0 = %v, want %v", status, got, want)
		}
	}
	if (OutputOptions{}).FailsOn(500) {
		t.Error("without --fail a 500 is refused")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A failed write is marked, so it exits 23 and not as a read error; a
// mark is not added twice.
func TestWriteErrors(t *testing.T) {
	if WriteError(nil) != nil {
		t.Error("nil became an error")
	}
	_, err := MarkWrites(failWriter{}).Write([]byte("x"))
	if !errors.Is(err, ErrWrite) || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("%v", err)
	}
	if again := WriteError(err); again != err {
		t.Error("a marked error was marked again")
	}
}

// An existing file stops the job unless --output-overwrite, but ghola's
// own partial file and an append may always reuse it.
func TestOpenOutput(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "a", "f")
	if _, err := OpenOutput(OutputOptions{}, name, os.O_CREATE|os.O_WRONLY, false); err == nil {
		t.Error("a missing folder was created without --output-create-dirs")
	}
	f, err := OpenOutput(OutputOptions{CreateDirs: true}, name, os.O_CREATE|os.O_WRONLY, false)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	keep := OutputOptions{NoOverwrite: true}
	if _, err := OpenOutput(keep, name, os.O_CREATE|os.O_WRONLY, false); !errors.Is(err, ErrWrite) || !strings.Contains(err.Error(), "--output-overwrite") {
		t.Errorf("an existing file: %v", err)
	}
	for _, c := range []struct {
		flags int
		own   bool
	}{{os.O_CREATE | os.O_WRONLY, true}, {os.O_APPEND | os.O_WRONLY, false}} {
		f, err := OpenOutput(keep, name, c.flags, c.own)
		if err != nil {
			t.Errorf("flags %x own %v: %v", c.flags, c.own, err)
			continue
		}
		f.Close()
	}
}

// --cond-etag-save writes the ETag of a 2xx answer, and keeps the file
// for any other status, so a 304 does not wipe the saved ETag.
func TestSaveETag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etag")
	o := OutputOptions{ETagSave: path}
	if err := o.SaveETag(200, `"v1"`); err != nil {
		t.Fatal(err)
	}
	if err := o.SaveETag(304, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "\"v1\"\n" {
		t.Errorf("after a 304: %q", b)
	}
	if err := o.SaveETag(200, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Errorf("a 200 without an ETag must empty the file: %q", b)
	}
	if err := (OutputOptions{}).SaveETag(200, "x"); err != nil {
		t.Error(err)
	}
}

// -o - is stdout, a value with a trailing / or an existing folder gets
// the remote name, and anything else is the file.
func TestSetTarget(t *testing.T) {
	dir := t.TempDir()
	for v, want := range map[string]string{"-": "stdout", "out/": "dir", dir: "dir", "f.txt": "file"} {
		var o OutputOptions
		o.File = "old"
		o.SetTarget(v)
		got := "file"
		switch {
		case o.Stdout:
			got = "stdout"
		case o.RemoteName && o.Dir == v:
			got = "dir"
		case o.File != v:
			got = "?"
		}
		if got != want {
			t.Errorf("-o %s: %s (%+v), want %s", v, got, o, want)
		}
	}
}

// --max-size stops a body past the size with ErrTooLarge (exit 63), on
// the write path, the read path and a declared length. A resume counts
// the bytes it has.
func TestMaxSize(t *testing.T) {
	o := OutputOptions{MaxSize: 4}
	if !o.TooLarge(5) || o.TooLarge(4) || o.TooLarge(-1) || (OutputOptions{}).TooLarge(1<<40) {
		t.Error("TooLarge")
	}
	var b bytes.Buffer
	w := o.LimitSize(&b, 1)
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("d")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("past the size: %v", err)
	}
	if (OutputOptions{}).LimitSize(&b, 0) != io.Writer(&b) {
		t.Error("no --max-size must not wrap")
	}
	if _, err := io.ReadAll(o.LimitReader(strings.NewReader("abcde"))); !errors.Is(err, ErrTooLarge) {
		t.Errorf("read past the size: %v", err)
	}
	if got, err := io.ReadAll(o.LimitReader(strings.NewReader("abcd"))); err != nil || string(got) != "abcd" {
		t.Errorf("read at the size: %q %v", got, err)
	}
	r := strings.NewReader("x")
	if (OutputOptions{}).LimitReader(r) != io.Reader(r) {
		t.Error("no --max-size must not wrap the reader")
	}
}

// --auth-aws-sigv4 takes curl's value. The region and the service come
// from the value or from an AWS host name; without either, the command
// fails at once instead of signing for a wrong region.
func TestParseSigV4(t *testing.T) {
	s, err := ParseSigV4("AWS:AMZ", "https://s3.eu-west-1.amazonaws.com/b")
	if err != nil || s.Provider1 != "aws" || s.Provider2 != "amz" {
		t.Fatalf("%+v %v", s, err)
	}
	if r, svc, err := s.RegionService("s3.eu-west-1.amazonaws.com"); err != nil || r != "eu-west-1" || svc != "s3" {
		t.Errorf("%s %s %v", r, svc, err)
	}
	s, err = ParseSigV4("aws", "")
	if err != nil || s.Provider2 != "aws" {
		t.Errorf("provider2 defaults to provider1: %+v %v", s, err)
	}
	if r, svc, err := (SigV4{Region: "r", Service: "s"}).RegionService("127.0.0.1"); err != nil || r != "r" || svc != "s" {
		t.Errorf("the value must win: %s %s %v", r, svc, err)
	}
	for _, bad := range [][2]string{{"", ""}, {"a:b:c:d:e", ""}, {":x", ""}, {"aws", "https://127.0.0.1/"}, {"aws", "https://localhost/"}} {
		if _, err := ParseSigV4(bad[0], bad[1]); err == nil {
			t.Errorf("%q for %q was accepted", bad[0], bad[1])
		}
	}
}

// The event log writes nothing without --output-events, and stamps each
// line with the time in UTC.
func TestEventLog(t *testing.T) {
	var nilLog *EventLog
	nilLog.Start("u") // must not panic
	var b bytes.Buffer
	l := NewEventLog(&b)
	l.now = func() time.Time { return time.Date(2026, 11, 21, 10, 0, 0, 5e6, time.FixedZone("X", 3600)) }
	l.Retry("u", 1, 3, 1500*time.Millisecond)
	l.End(TransferInfo{URL: "u", ExitCode: 28})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"time":"2026-11-21T09:00:00.005Z"`) || !strings.Contains(lines[0], `"wait_ms":1500`) {
		t.Errorf("%q", lines)
	}
	if !strings.Contains(lines[1], `"event":"error"`) || !strings.Contains(lines[1], `"error":"exit code 28"`) {
		t.Errorf("an error without a message must still name a cause: %s", lines[1])
	}
}

// Small helpers that the command line and the download share.
func TestHelpers(t *testing.T) {
	if NormalizeURL("localhost:8080/x") != "http://localhost:8080/x" || NormalizeURL("ftp://h/") != "ftp://h/" {
		t.Error("NormalizeURL")
	}
	names := ChainNames()
	for _, n := range names {
		if _, ok := LookupChain(n); !ok {
			t.Errorf("chain %s does not look up", n)
		}
	}
	if len(names) == 0 {
		t.Error("no chain names")
	}
	o := &Options{URL: "https://h/a/file.zip"}
	o.Output.Dir, o.Output.File = "dl", "old"
	TakeRemoteName(o)
	if o.Output.File != filepath.Join("dl", "file.zip") {
		t.Errorf("remote name %q", o.Output.File)
	}
	var info *TransferInfo
	info.AddSize(1) // nil-safe: no template
	info = &TransferInfo{}
	info.AddSize(3)
	info.AddSize(4)
	if info.Size != 7 {
		t.Errorf("size %d", info.Size)
	}
	ue := &UsageError{Err: errors.New("bad"), Usage: "u"}
	if ue.Error() != "bad" || !errors.Is(ue, ue.Err) {
		t.Error("UsageError")
	}
}

// %{stderr} switches the template output to stderr, and %{json} holds
// every variable.
func TestTemplate_StderrAndJSON(t *testing.T) {
	tpl, err := ParseTemplate("a%{stderr}%{http_code}%{stdout}%{json}")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := tpl.Write(&out, &errOut, TransferInfo{Status: 200, URL: "u"}); err != nil {
		t.Fatal(err)
	}
	if errOut.String() != "200" || !strings.HasPrefix(out.String(), "a{") || !strings.Contains(out.String(), `"url":"u"`) {
		t.Errorf("stdout %q stderr %q", out.String(), errOut.String())
	}
}
