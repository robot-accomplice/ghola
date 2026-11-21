package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A server must not redirect ghola to a local file. curl follows only
// http, https, ftp and ftps. Now that ghola reads file:// URLs, a redirect
// to file:// would read the file, so it is refused on both paths.
func TestRedirectToFileIsRefused(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("do not read"), 0o644) //nolint:errcheck
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///"+strings.TrimPrefix(filepath.ToSlash(secret), "/"), http.StatusFound)
	}))
	defer ts.Close()
	follow := config.ResilienceOptions{Location: true, MaxRedirs: 5}

	t.Run("stdout", func(t *testing.T) {
		_, _, err := FetchURL(context.Background(), &config.Options{URL: ts.URL, Method: "GET", Resilience: follow}, nil)
		if !errors.Is(err, ErrRedirectScheme) {
			t.Fatalf("err = %v, want ErrRedirectScheme", err)
		}
	})
	t.Run("download", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "f")
		opts := &config.Options{URL: ts.URL, Method: "GET", Resilience: follow, Output: config.OutputOptions{File: out, Silent: true}}
		if err := Download(context.Background(), opts, nil); !errors.Is(err, ErrRedirectScheme) {
			t.Fatalf("err = %v, want ErrRedirectScheme", err)
		}
		if b, _ := os.ReadFile(out); strings.Contains(string(b), "do not read") {
			t.Fatal("the local file was copied")
		}
	})
}

// A file:// download resumes, as an HTTP download does: the file transport
// answers the Range with 206.
func TestDownload_FileURLResumes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.WriteFile(src, []byte("0123456789"), 0o644) //nolint:errcheck
	out := filepath.Join(dir, "out")
	os.WriteFile(out, []byte("0123"), 0o644) //nolint:errcheck
	opts := &config.Options{URL: "file:///" + strings.TrimPrefix(filepath.ToSlash(src), "/"), Method: "GET",
		Output: config.OutputOptions{File: out, ContinueAt: "-", Silent: true}}
	if err := Download(context.Background(), opts, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "0123456789" {
		t.Errorf("file = %q", b)
	}
}
