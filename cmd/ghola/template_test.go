package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gholaStdout runs ghola and returns its exit code and stdout.
func gholaStdout(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	code := execute(context.Background(), args, nil, nil, out)
	b, _ := os.ReadFile(out.Name())
	return code, string(b)
}

// --output-template writes curl's -w variables after each transfer, also
// after a failure: a script reads the status, the final URL and the size
// without parsing stderr.
// redirectServer redirects /r to /f, which answers "hello"; any other
// path is a 404.
func redirectServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/r":
			http.Redirect(w, r, "/f", http.StatusFound)
		case "/f":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "hello")
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestExecute_OutputTemplate(t *testing.T) {
	srv := redirectServer()
	defer srv.Close()
	const tmpl = `%{http_code} %{num_redirects} %{size_download} %{content_type} %{url_effective} %{exitcode}\n`
	want := "200 1 5 text/plain " + srv.URL + "/f 0\n"
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"fetch -o", []string{"-q", "--output-template", tmpl, "-o", filepath.Join(dir, "a"), srv.URL + "/r"}, want},
		{"download", []string{"download", "-q", "--output-template", tmpl, "-o", filepath.Join(dir, "b"), srv.URL + "/r"}, want},
		{"stdout", []string{"-q", "--output-template", tmpl, srv.URL + "/r"}, "hello" + want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, got := gholaStdout(t, tc.args...)
			if code != 0 || got != tc.want {
				t.Errorf("exit %d, stdout %q, want %q", code, got, tc.want)
			}
		})
	}
	code, got := gholaStdout(t, "-q", "--output-template", `%{json}`, srv.URL+"/missing")
	var j map[string]any
	if err := json.Unmarshal([]byte(got), &j); err != nil {
		t.Fatalf("after a 404: %q is not JSON: %v", got, err)
	}
	if code != 22 || j["http_code"] != "404" || j["exitcode"] != float64(22) || !strings.Contains(fmt.Sprint(j["errormsg"]), "404") {
		t.Errorf("after a 404: exit %d, json %v", code, j)
	}
}
