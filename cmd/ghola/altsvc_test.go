package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --alt-svc writes each Alt-Svc header of an https answer to the cache in
// curl's format, on both stacks and for fetch and download (decision K).
// curl reads the same file, so the line format is the contract.
func TestExecute_AltSvcCache(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Alt-Svc", `h3=":443"; ma=600`)
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"default":     {"-q", "-k"},
		"impersonate": {"-q", "-k", "--impersonate", "chrome"},
		"download":    {"download", "-q", "-k", "-o", filepath.Join(dir, "f")},
	} {
		cache := filepath.Join(dir, name+".txt")
		if code := runGhola(t, append(args, "--alt-svc", cache, ts.URL+"/")...); code != 0 {
			t.Errorf("%s: exit %d", name, code)
			continue
		}
		data, err := os.ReadFile(cache)
		want := " " + u.Hostname() + " " + u.Port() + " h3 " + u.Hostname() + " 443 \""
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("%s: cache %q (%v), want a line with %q", name, data, err, want)
		}
	}
}
