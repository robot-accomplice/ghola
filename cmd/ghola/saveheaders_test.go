package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --save-headers writes the head of each response, the redirect too, as
// curl -D does, on fetch and on download. The body does not go into it.
func TestExecute_SaveHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r" {
			http.Redirect(w, r, "/f", http.StatusFound)
			return
		}
		w.Header().Set("X-Mark", "final")
		fmt.Fprint(w, "the body")
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, job := range []string{"fetch", "download"} {
		t.Run(job, func(t *testing.T) {
			heads := filepath.Join(dir, job+".heads")
			args := []string{"-q", "--follow", "--save-headers", heads, "-o", filepath.Join(dir, job+".body"), srv.URL + "/r"}
			if job == "download" {
				args = append([]string{job}, args...)
			}
			out, _ := os.CreateTemp(t.TempDir(), "out")
			defer out.Close()
			if code := execute(context.Background(), args, nil, nil, out); code != 0 {
				t.Fatalf("exit %d", code)
			}
			b, err := os.ReadFile(heads)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			i302, i200 := strings.Index(got, " 302 "), strings.Index(got, " 200 ")
			if i302 < 0 || i200 < i302 || !strings.Contains(got, "X-Mark: final\r\n") || strings.Contains(got, "the body") {
				t.Errorf("header file:\n%s", got)
			}
		})
	}
}
