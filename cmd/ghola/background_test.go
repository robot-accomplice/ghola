package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --log sends ghola's messages to a file, appended, as wget -a does.
func TestExecute_Log(t *testing.T) {
	log := filepath.Join(t.TempDir(), "run.log")
	os.WriteFile(log, []byte("before\n"), 0o600) //nolint:errcheck
	if code := runGhola(t, "-r", "0", "--log", log, deadURL(t)); code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
	b, _ := os.ReadFile(log)
	if !strings.HasPrefix(string(b), "before\n") || !strings.Contains(string(b), "Failed:") {
		t.Errorf("log:\n%s", b)
	}
}

// --background returns at once, and the job goes on in a new process
// that writes to ghola.log in the folder, as wget -b does. The test
// builds ghola, because the test binary cannot start itself as ghola.
func TestExecute_Background(t *testing.T) {
	if testing.Short() {
		t.Skip("builds ghola")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond) // the parent must not wait for this
		fmt.Fprint(w, "late body")
	}))
	defer srv.Close()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ghola.exe")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "download", "--background", "-o", "f", srv.URL)
	cmd.Dir = dir
	start := time.Now()
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "Continuing in background") {
		t.Fatalf("%v: %s", err, out)
	}
	if d := time.Since(start); d > 250*time.Millisecond+2*time.Second {
		t.Errorf("--background took %v; it must not wait for the job", d)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, _ := os.ReadFile(filepath.Join(dir, "f")); string(b) == "late body" {
			if _, err := os.Stat(filepath.Join(dir, "ghola.log")); err != nil {
				t.Errorf("no ghola.log: %v", err)
			}
			return
		}
	}
	t.Fatal("the background job did not save the file")
}
