package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// --cookie and --cookie-jar work on a download to a file, as on stdout.
// Before, only FetchURL (stdout) used the jar, so -o sent no cookie and
// saved none: the H8 pattern of a flag that reaches one path only.
func TestDownload_UsesTheCookieJar(t *testing.T) {
	var mu sync.Mutex
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Cookie"))
		mu.Unlock()
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
			http.Redirect(w, r, "/file", http.StatusFound)
			return
		}
		w.Write([]byte("file")) //nolint:errcheck
	}))
	defer ts.Close()

	dir := t.TempDir()
	jar := filepath.Join(dir, "jar.txt")
	opts := &config.Options{
		URL:        ts.URL + "/login",
		Stealth:    config.StealthOptions{CookieJar: jar, Cookies: []string{"seed=0"}},
		Resilience: config.ResilienceOptions{Location: true, MaxRedirs: 5},
		Output:     config.OutputOptions{File: filepath.Join(dir, "f"), Silent: true},
	}
	if err := Download(context.Background(), opts, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "seed=0" || !strings.Contains(got[1], "session=s1") {
		t.Errorf("Cookie headers = %q; want the seed on the first request and the session after the redirect", got)
	}
	if b, _ := os.ReadFile(jar); !strings.Contains(string(b), "session\ts1") {
		t.Errorf("the jar lacks the session cookie:\n%s", b)
	}
}
