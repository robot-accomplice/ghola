package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The server chooses the Content-Disposition name and the URL name. Each
// file that ghola writes must stay inside the -o folder, on every OS and on
// every job: fetch with -o DIR/, download, and mirror.
func TestExecute_ServerNamesStayInTheOutputFolder(t *testing.T) {
	work := t.TempDir()
	out := filepath.Join(work, "a", "b", "o")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(work, "abs.txt")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := r.URL.Query().Get("cd"); n != "" {
			w.Header().Set("Content-Disposition", `attachment; filename="`+n+`"`)
		}
		w.Write([]byte("PAYLOAD\n")) //nolint:errcheck
	}))
	defer srv.Close()

	cd := func(name string) string { return srv.URL + "/f?cd=" + urlQuery(name) }
	cases := [][]string{
		{"--output-name", "header", "-o", out + string(filepath.Separator), cd(`..\..\up-cd.txt`)},
		{"--output-name", "header", "-o", out + string(filepath.Separator), cd(abs)},
		{"--output-name", "header", "-o", out + string(filepath.Separator), cd(`ads.txt:hidden`)},
		{"download", "--output-name", "header", "-o", out + string(filepath.Separator), cd(`C:..\drive-rel.txt`)},
		{"download", "-o", out + string(filepath.Separator), srv.URL + "/x/..%5C..%5Cup-url.txt"},
	}
	for _, args := range cases {
		runGhola(t, quiet(args)...)
	}
	err := filepath.Walk(work, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasPrefix(p, out+string(filepath.Separator)) {
			t.Errorf("ghola wrote %s, outside %s", p, out)
		}
		if runtime.GOOS == "windows" && strings.Contains(filepath.Base(p), ":") { // ":" writes an NTFS stream; it is a plain byte elsewhere
			t.Errorf("ghola wrote %s, a name with a stream", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F", `\`, "%5C", ":", "%3A")
	return r.Replace(s)
}
