package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --trace writes the request and the answer, header and body, as curl
// --trace does: in plain bytes also on TLS, on stdout and on -o, in hex
// or with --trace-format ascii. This is how one sees what went on the
// wire when an answer is wrong.
func TestExecute_Trace(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "body-bytes")
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"ascii, stdout", []string{"--trace-format", "ascii"}, []string{"=> Send header", "0000: GET / HTTP/1.1", "<= Recv header", "<= Recv data", "body-bytes"}},
		{"hex, -o", []string{"-o", filepath.Join(dir, "f")}, []string{"=> Send header", "0000: 47 45 54 20", "<= Recv data", "62 6f 64 79"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := filepath.Join(dir, tc.name+".trace")
			args := append([]string{"-q", "-k", "--trace", trace}, tc.args...)
			if code := runGhola(t, append(args, srv.URL+"/")...); code != 0 {
				t.Fatalf("exit %d", code)
			}
			b, _ := os.ReadFile(trace)
			for _, w := range tc.want {
				if !strings.Contains(string(b), w) {
					t.Errorf("trace has no %q:\n%s", w, b)
				}
			}
		})
	}
}
