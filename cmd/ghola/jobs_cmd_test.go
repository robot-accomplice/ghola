package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A download that stops keeps an entry in the job list, and "ghola jobs
// resume" finishes it with -c and the options of its first run, as aria2
// --save-session does; then the entry is gone. pause keeps a job out of
// resume, and cancel removes it with its partial file. --no-session keeps
// no entry.
func TestExecute_JobsCommand(t *testing.T) {
	// A job list of its own: the other tests leave failed downloads in
	// the shared one, and resume would run them all.
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
	body := bytes.Repeat([]byte("0123456789"), 20000)
	var broken atomic.Bool
	var sawHeader atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader.Store(r.Header.Get("X-Job"))
		if broken.Load() && r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("ETag", `"v1"`)
			w.Write(body[:len(body)/2]) //nolint:errcheck
			panic(http.ErrAbortHandler) // the connection breaks halfway
		}
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	broken.Store(true)
	if runGhola(t, "download", "-q", "-r", "0", "-H", "X-Job: kept", "-o", file, srv.URL+"/f") == 0 {
		t.Fatal("the broken download exited 0")
	}
	code, list := gholaStdout(t, "jobs", "list")
	if code != 0 || !strings.Contains(list, file) || !strings.Contains(list, "unfinished") {
		t.Fatalf("jobs list: exit %d\n%s", code, list)
	}
	broken.Store(false)
	sawHeader.Store("")
	if code := runGhola(t, "jobs", "resume"); code != 0 {
		t.Fatalf("jobs resume: exit %d", code)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, body) {
		t.Errorf("after resume: %d bytes, want %d", len(got), len(body))
	}
	if h, _ := sawHeader.Load().(string); h != "kept" {
		t.Errorf("resume sent X-Job %q; the options of the first run must come back", h)
	}
	if _, list := gholaStdout(t, "jobs", "list"); strings.Contains(list, file) {
		t.Errorf("a finished job is still listed:\n%s", list)
	}

	broken.Store(true)
	other := filepath.Join(dir, "g")
	runGhola(t, "download", "-q", "-r", "0", "-o", other, srv.URL+"/g")
	_, list = gholaStdout(t, "jobs", "list")
	id := strings.Fields(list)[0]
	if code := runGhola(t, "jobs", "pause", id); code != 0 {
		t.Fatalf("pause: exit %d", code)
	}
	broken.Store(false)
	runGhola(t, "jobs", "resume")
	if fi, _ := os.Stat(other); fi.Size() == int64(len(body)) {
		t.Error("resume ran a paused job")
	}
	if code := runGhola(t, "jobs", "cancel", id); code != 0 {
		t.Fatalf("cancel: exit %d", code)
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("cancel kept the partial file")
	}
	broken.Store(true)
	runGhola(t, "download", "-q", "-r", "0", "--no-session", "-o", filepath.Join(dir, "h"), srv.URL+"/h")
	if _, list := gholaStdout(t, "jobs", "list"); list != "" {
		t.Errorf("jobs left after --no-session and cancel:\n%s", list)
	}
}
