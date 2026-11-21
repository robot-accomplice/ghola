package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A config file applies to a real transfer, and the command line wins.
func TestExecute_ConfigFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("X-A")+r.Header.Get("X-B"))
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("header = X-A: from-config\nquiet\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(t.TempDir(), "body")
	out, _ := os.CreateTemp(t.TempDir(), "out")
	defer out.Close()
	if code := execute(context.Background(), []string{"--config", cfg, "-H", "X-B: -cli", "-o", body, srv.URL}, nil, nil, out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if b, _ := os.ReadFile(body); string(b) != "from-config-cli" {
		t.Errorf("body = %q", b)
	}
	if code := execute(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "none"), srv.URL}, nil, nil, out); code != 2 {
		t.Errorf("a missing --config: exit %d, want 2", code)
	}
}
