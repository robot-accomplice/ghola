package client

// These tests check that a download to a file (-o, ghola download) keeps
// the same rules as curl -o: --no-follow, the --fail rule, the result of a
// failed decode or checksum, and -c on a file that is complete.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

func fileOpts(url, file string) *config.Options {
	return &config.Options{URL: url, Method: "GET", Concurrency: 1,
		Resilience: config.ResilienceOptions{Location: true, MaxRedirs: 5},
		Output:     config.OutputOptions{File: file, Silent: true, Fail: true, FailFrom400: true}}
}

// --no-follow: the redirect answer is the result, as curl -o without -L
// writes it. Before the fix the download followed the redirect anyway.
func TestDownload_NoFollowWritesTheRedirectAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.Write(bytes.Repeat([]byte("final "), 300000)) //nolint:errcheck // large enough to segment
			return
		}
		w.Header().Set("Location", "/final")
		w.WriteHeader(http.StatusFound)
		w.Write([]byte("moved")) //nolint:errcheck
	}))
	defer ts.Close()
	for _, n := range []int{1, 4} {
		file := filepath.Join(t.TempDir(), "f")
		opts := fileOpts(ts.URL+"/start", file)
		opts.Concurrency = n
		opts.Resilience.Location = false
		if err := Download(context.Background(), opts, nil); err != nil {
			t.Fatalf("-n %d: %v", n, err)
		}
		if got, _ := os.ReadFile(file); string(got) != "moved" {
			t.Errorf("-n %d: the file is %d bytes, not the redirect body: the redirect was followed", n, len(got))
		}
	}
}

// The --fail rule of a download is curl's: 400 and above. A 3xx without
// a Location, or a 304, is written and exits 0. --no-fail writes a 404 body
// and exits 0, as curl -o without -f does.
func TestDownload_FailRuleIsFrom400(t *testing.T) {
	cases := []struct {
		status  int
		fail    bool
		wantErr bool
	}{
		{399, true, false},
		{304, true, false},
		{404, true, true},
		{404, false, false},
		{500, false, false},
	}
	for _, c := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			if c.status != http.StatusNotModified {
				w.Write([]byte("page")) //nolint:errcheck
			}
		}))
		file := filepath.Join(t.TempDir(), "f")
		opts := fileOpts(ts.URL, file)
		opts.Output.Fail = c.fail
		err := Download(context.Background(), opts, nil)
		ts.Close()
		if gotErr := err != nil; gotErr != c.wantErr {
			t.Errorf("status %d, fail=%v: err = %v, want error %v", c.status, c.fail, err, c.wantErr)
			continue
		}
		if c.wantErr && !errors.Is(err, config.ErrHTTPStatus) {
			t.Errorf("status %d: err = %v, want ErrHTTPStatus", c.status, err)
		}
	}
}

// A body that does not decode is a content encoding error (curl exits 61),
// and it leaves no file: an empty or cut file would look like a result.
func TestDownload_BadEncodingLeavesNoFile(t *testing.T) {
	for _, ce := range []string{"deflate", "br", "zstd"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Encoding", ce)
			w.Write(bytes.Repeat([]byte{0xff, 0x00, 0x13}, 400)) //nolint:errcheck // not a valid stream
		}))
		file := filepath.Join(t.TempDir(), "f")
		opts := fileOpts(ts.URL, file)
		opts.Stealth.Compressed = true
		err := Download(context.Background(), opts, nil)
		ts.Close()
		if !errors.Is(err, gholatransport.ErrBadEncoding) {
			t.Errorf("%s: err = %v, want ErrBadEncoding", ce, err)
		}
		if _, serr := os.Stat(file); !os.IsNotExist(serr) {
			t.Errorf("%s: a file stays after the decode error", ce)
		}
	}
}

// A download that does not match --checksum must not stay under the final
// name, where it looks like the verified file. Its state file goes too, so
// a resume does not take the bad bytes as a finished file.
func TestDownload_ChecksumMismatchLeavesNoFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "f", time.Unix(1700000000, 0), bytes.NewReader([]byte("the body")))
	}))
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	opts := fileOpts(ts.URL, file)
	d, err := config.ParseChecksum("sha-256=" + string(bytes.Repeat([]byte("0"), 64)))
	if err != nil {
		t.Fatal(err)
	}
	opts.Output.Checksum = d
	if err := Download(context.Background(), opts, nil); !errors.Is(err, config.ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	for _, f := range []string{file, statePath(file)} {
		if _, serr := os.Stat(f); !os.IsNotExist(serr) {
			t.Errorf("%s stays after the checksum mismatch", filepath.Base(f))
		}
	}
}

// -c on a file that is complete is a success, as in curl, Wget2 and aria2:
// the server answers 416 for a range that starts at the end.
func TestDownload_ContinueOnCompleteFileSucceeds(t *testing.T) {
	data := []byte("the whole file")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Unix(1700000000, 0), bytes.NewReader(data))
	}))
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	opts := fileOpts(ts.URL, file)
	opts.Output.ContinueAt = "-"
	if err := Download(context.Background(), opts, nil); err != nil {
		t.Fatalf("-c on a complete file: %v", err)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, data) {
		t.Errorf("file = %q, want it unchanged", got)
	}

	// A local file that is longer than the remote file is not complete.
	if err := os.WriteFile(file, append(data, "more"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), opts, nil); err == nil {
		t.Error("-c on a file longer than the remote file succeeded")
	}
}
