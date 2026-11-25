package client

// These tests check that a segmented download (-n, --source) writes a file
// only from pieces of one version of the remote file, and that a resume
// after a killed segmented download does not take its holes as data.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// bigBody is a version of a file that is large enough to segment.
func bigBody(version int) []byte {
	return bytes.Repeat([]byte(fmt.Sprintf("v%d-0123456789abcdef.", version)), 80000) // 1.68 MB
}

// serveVersion answers as a static file server: Range, If-Range and an ETag.
func serveVersion(w http.ResponseWriter, r *http.Request, version int) {
	w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, version))
	http.ServeContent(w, r, "f", time.Unix(1700000000+int64(version), 0), bytes.NewReader(bigBody(version)))
}

func segmentOpts(url, file string, n int) *config.Options {
	return &config.Options{
		URL: url, Method: "GET", Concurrency: n,
		Output:  config.OutputOptions{File: file, Silent: true},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
}

// A server that sends fewer bytes than its Content-Range names must not
// give a file with a hole that ghola reports as complete.
func TestSegmented_ShortSegmentBodyFails(t *testing.T) {
	data := testData()
	short := func(ctx *fasthttp.RequestCtx, data []byte, start, end int) {
		ctx.SetStatusCode(fasthttp.StatusPartialContent)
		ctx.Response.Header.Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end)+"/"+itoa(len(data)))
		ctx.SetBody(data[start : start+(end-start+1)/2]) // half of the named range
	}
	out := filepath.Join(t.TempDir(), "f")
	err := Download(context.Background(), downloadOpts(out, 4, ""), rangeLiar(t, data, short))
	if err == nil {
		got, _ := os.ReadFile(out)
		t.Fatalf("short segments accepted: exit 0 with a %d-byte file, equal=%v", len(got), bytes.Equal(got, data))
	}
}

// A --source mirror that holds another version of the file (another ETag)
// must not add its bytes to the file.
func TestSegmented_MirrorWithOtherVersionFails(t *testing.T) {
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveVersion(w, r, 1) }))
	defer main.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveVersion(w, r, 2) }))
	defer mirror.Close()

	out := filepath.Join(t.TempDir(), "f")
	opts := segmentOpts(main.URL, out, 1)
	opts.Sources = []string{mirror.URL}
	err := Download(context.Background(), opts, nil)
	got, _ := os.ReadFile(out)
	if err == nil && !bytes.Equal(got, bigBody(1)) {
		t.Fatalf("a mirror with another ETag was mixed in: exit 0, file is not version 1")
	}
	if err == nil {
		t.Fatal("a mirror with another ETag was accepted")
	}
}

// A --source mirror whose file has another size must not be used.
func TestSegmented_MirrorWithOtherSizeFails(t *testing.T) {
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveVersion(w, r, 1) }))
	defer main.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Unix(1700000001, 0), bytes.NewReader(bigBody(1)[1000:]))
	}))
	defer mirror.Close()

	out := filepath.Join(t.TempDir(), "f")
	opts := segmentOpts(main.URL, out, 1)
	opts.Sources = []string{mirror.URL}
	if err := Download(context.Background(), opts, nil); err == nil {
		t.Fatal("a mirror with another size was accepted")
	}
}

// When the remote file changes during a segmented download, the file must
// not join pieces of two versions. The segments send If-Range, so the
// server sends the whole new version, and ghola starts again.
func TestSegmented_FileChangesDuringDownload(t *testing.T) {
	var requests atomic.Int64
	var ifRange atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Range") != "" {
			ifRange.Add(1)
		}
		version := 1
		if requests.Add(1) > 2 { // the probe and one segment see version 1
			version = 2
		}
		serveVersion(w, r, version)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "f")
	err := Download(context.Background(), segmentOpts(srv.URL, out, 4), nil)
	got, _ := os.ReadFile(out)
	if err == nil && !bytes.Equal(got, bigBody(1)) && !bytes.Equal(got, bigBody(2)) {
		t.Fatal("exit 0 with a file that joins two versions")
	}
	if ifRange.Load() == 0 {
		t.Error("no segment sent If-Range")
	}
}

// blockingServer serves one version and holds the request for the piece
// that starts at hold until release closes.
type blockingServer struct {
	hold    string
	release chan struct{}
}

func (s *blockingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Range"), "bytes="+s.hold+"-") {
		select {
		case <-s.release:
		case <-r.Context().Done():
			return
		}
	}
	serveVersion(w, r, 1)
}

// stateDone returns the number of finished pieces in file's state file.
func stateDone(file string) int {
	b, err := os.ReadFile(statePath(file))
	if err != nil {
		return -1
	}
	var s resumeState
	if json.Unmarshal(b, &s) != nil {
		return -1
	}
	return len(s.Done)
}

// copyFile copies src to dst, as a kill would leave it.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A segmented download that is killed leaves a file with holes. Its state
// file must say which pieces are done, so that -c does not take the
// holes for data and append after them.
func TestSegmented_ResumeAfterKillFillsTheHoles(t *testing.T) {
	size := len(bigBody(1))
	pieces := splitRanges(int64(size), 4)
	srv := &blockingServer{hold: fmt.Sprint(pieces[1].start), release: make(chan struct{})}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "f")
	opts := segmentOpts(ts.URL, out, 4)
	opts.Output.FileAllocation = "trunc" // the file has its full size at once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = Download(context.Background(), opts, nil)
	}()

	// Wait until every piece but the held one is done, then copy the file
	// and its state, as a kill at this moment leaves them.
	deadline := time.Now().Add(10 * time.Second)
	for stateDone(out) < len(pieces)-1 {
		if time.Now().After(deadline) {
			close(srv.release)
			wg.Wait()
			t.Fatalf("no state file with %d done pieces during the download (got %d)", len(pieces)-1, stateDone(out))
		}
		time.Sleep(10 * time.Millisecond)
	}
	killed := filepath.Join(dir, "killed")
	copyFile(t, out, killed)
	copyFile(t, statePath(out), statePath(killed))
	close(srv.release)
	wg.Wait()

	resume := segmentOpts(ts.URL, killed, 1)
	resume.Output.ContinueAt = "-"
	if err := Download(context.Background(), resume, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got, _ := os.ReadFile(killed); !bytes.Equal(got, bigBody(1)) {
		t.Fatalf("the resumed file is not the remote file (%d bytes, want %d)", len(got), size)
	}
}

// The state file must not keep the user name and password of the URL:
// any user who can read the folder could read them.
func TestResumeState_HasNoURLCredentials(t *testing.T) {
	srv := &changingServer{}
	ts := httptest.NewTLSServer(srv)
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "f")
	url := strings.Replace(ts.URL, "https://", "https://bob:UrlPW@", 1)
	interrupted(t, srv, url, file, "")
	st, err := os.ReadFile(statePath(file))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(st), "UrlPW") || strings.Contains(string(st), "bob") {
		t.Errorf("state file holds the URL credentials: %s", st)
	}
}
