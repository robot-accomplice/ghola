package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// changingServer serves one version of a file at a time, with an ETag for
// each version. http.ServeContent answers Range and If-Range as RFC 9110
// says. When breakFirst is set, a request without Range gets half the body
// and a broken connection, as an interrupted download does.
type changingServer struct {
	mu         sync.Mutex
	version    int
	breakFirst bool
	ifRanges   []string // the If-Range header of each request, "" when absent
}

func body(version int) []byte {
	return bytes.Repeat([]byte(fmt.Sprintf("version %d. ", version)), 30000)
}

func (s *changingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	v, brk := s.version, s.breakFirst
	s.ifRanges = append(s.ifRanges, r.Header.Get("If-Range"))
	s.mu.Unlock()
	b := body(v)
	w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, v))
	if brk && r.Header.Get("Range") == "" {
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.Write(b[:len(b)/2]) //nolint:errcheck
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}
	http.ServeContent(w, r, "f", time.Unix(1700000000, 0), bytes.NewReader(b))
}

func (s *changingServer) set(version int, breakFirst bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version, s.breakFirst = version, breakFirst
}

func (s *changingServer) lastIfRange() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ifRanges[len(s.ifRanges)-1]
}

func resumeOpts(url, file, impersonate, continueAt string) *config.Options {
	return &config.Options{
		URL:     url,
		Stealth: config.StealthOptions{Impersonate: impersonate, Insecure: true},
		Output:  config.OutputOptions{File: file, ContinueAt: continueAt, Silent: true},
	}
}

// interrupted runs a download that the server breaks off, and checks that
// ghola kept the partial file and the state file that names its version.
func interrupted(t *testing.T, srv *changingServer, url, file, imp string) {
	t.Helper()
	srv.set(1, true)
	if err := Download(context.Background(), resumeOpts(url, file, imp, ""), nil); err == nil {
		t.Fatal("the broken download succeeded")
	}
	st, err := os.ReadFile(statePath(file))
	if err != nil {
		t.Fatalf("no state file after the broken download: %v", err)
	}
	if !strings.Contains(string(st), `\"v1\"`) {
		t.Fatalf("state file %s does not name version 1", st)
	}
}

// A resume of an unchanged file appends the rest. The request carries the
// ETag of the partial file's version, so the server can say if it changed.
func TestResume_UnchangedFileAppendsTheRest(t *testing.T) {
	for _, imp := range []string{"", "chrome"} {
		t.Run("impersonate="+imp, func(t *testing.T) {
			srv := &changingServer{}
			ts := httptest.NewTLSServer(srv)
			defer ts.Close()
			file := filepath.Join(t.TempDir(), "f")
			interrupted(t, srv, ts.URL, file, imp)

			srv.set(1, false)
			if err := Download(context.Background(), resumeOpts(ts.URL, file, imp, "-"), nil); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if got := srv.lastIfRange(); got != `"v1"` {
				t.Errorf("If-Range = %q, want %q", got, `"v1"`)
			}
			if got, _ := os.ReadFile(file); !bytes.Equal(got, body(1)) {
				t.Errorf("file is %d bytes, not version 1 (%d bytes)", len(got), len(body(1)))
			}
			if _, err := os.Stat(statePath(file)); !os.IsNotExist(err) {
				t.Errorf("the state file stays after the download finished: %v", err)
			}
		})
	}
}

// A resume after the remote file changed must not join the old partial
// bytes to the new version (curl, Wget2 and aria2 all do). The server sees
// the old ETag in If-Range and sends the whole new version, and ghola writes
// it from byte 0.
func TestResume_ChangedFileStartsAgain(t *testing.T) {
	for _, imp := range []string{"", "chrome"} {
		t.Run("impersonate="+imp, func(t *testing.T) {
			srv := &changingServer{}
			ts := httptest.NewTLSServer(srv)
			defer ts.Close()
			file := filepath.Join(t.TempDir(), "f")
			interrupted(t, srv, ts.URL, file, imp)

			srv.set(2, false)
			if err := Download(context.Background(), resumeOpts(ts.URL, file, imp, "-"), nil); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if got, _ := os.ReadFile(file); !bytes.Equal(got, body(2)) {
				t.Errorf("file is %d bytes and not version 2: the old part was kept", len(got))
			}
		})
	}
}

// Without a state file (a partial file from another tool), ghola resumes as
// curl does: it sends no If-Range, because it has no version to name.
func TestResume_WithoutStateSendsNoIfRange(t *testing.T) {
	srv := &changingServer{}
	srv.set(1, false)
	ts := httptest.NewTLSServer(srv)
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, body(1)[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), resumeOpts(ts.URL, file, "", "-"), nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := srv.lastIfRange(); got != "" {
		t.Errorf("If-Range = %q, want none", got)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, body(1)) {
		t.Errorf("file is %d bytes, not version 1", len(got))
	}
}

// A weak ETag cannot be used in If-Range, so the validator is Last-Modified.
func TestResumeState_WeakETagUsesLastModified(t *testing.T) {
	s := resumeState{ETag: `W/"x"`, LastModified: "Tue, 14 Nov 2023 22:13:20 GMT"}
	if got := s.validator(); got != s.LastModified {
		t.Errorf("validator = %q, want Last-Modified", got)
	}
	if got := (resumeState{ETag: `"x"`, LastModified: s.LastModified}).validator(); got != `"x"` {
		t.Errorf("validator = %q, want the strong ETag", got)
	}
}
