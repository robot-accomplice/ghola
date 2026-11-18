package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// TestExecute_ProxyFromEnvironment checks that HTTP_PROXY and NO_PROXY apply
// on stdout, on -o and on --impersonate (audit finding M8). The proxy is
// dead, so a request that uses it fails and never reaches the server. Go's
// own rule skips the proxy for 127.0.0.1; curl does not, and ghola follows
// curl.
func TestExecute_ProxyFromEnvironment(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write([]byte("ok")) //nolint:errcheck
	}))
	defer srv.Close()
	dead := deadURL(t)

	modes := map[string][]string{
		"stdout":      nil,
		"-o":          {"-o", filepath.Join(t.TempDir(), "f")},
		"impersonate": {"--impersonate", "chrome"},
	}
	for name, extra := range modes {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{"-s"}, extra...), srv.URL)
			for _, tc := range []struct {
				noProxy  string
				wantHits int32
			}{
				{"", 0},          // the dead proxy stops the request
				{"127.0.0.1", 1}, // NO_PROXY sends it direct
			} {
				t.Setenv("HTTP_PROXY", dead)
				t.Setenv("NO_PROXY", tc.noProxy)
				hits.Store(0)
				out, err := os.CreateTemp(t.TempDir(), "out")
				if err != nil {
					t.Fatal(err)
				}
				code := execute(context.Background(), args, nil, nil, out)
				out.Close()
				if got := hits.Load(); got != tc.wantHits {
					t.Errorf("NO_PROXY=%q: server got %d requests, want %d (exit %d)", tc.noProxy, got, tc.wantHits, code)
				}
				if (code == 0) != (tc.wantHits == 1) {
					t.Errorf("NO_PROXY=%q: exit %d", tc.noProxy, code)
				}
			}
		})
	}
}

// TestExecute_ProxyFlagOnDownload checks that --proxy applies to a download
// to a file too. The download path built its transport without the proxy.
func TestExecute_ProxyFlagOnDownload(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	args := []string{"-s", "--proxy", deadURL(t), "-o", filepath.Join(t.TempDir(), "f"), srv.URL}
	if code := execute(context.Background(), args, nil, nil, nil); code == 0 || hits.Load() != 0 {
		t.Errorf("ghola %q: exit %d, server got %d requests; want a failure and none", args, code, hits.Load())
	}
}
