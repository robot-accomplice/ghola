package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// --checksum checks the body against a digest, and the server's
// Content-Digest is checked by default: a corrupt download exits 32
// (aria2's code) and does not pass as done. The file stays, since the
// digest may be the wrong one. A Content-Digest of an encoded body is not
// compared with the decoded file.
func TestExecute_Checksum(t *testing.T) {
	const body = "the file"
	sum := sha256.Sum256([]byte(body))
	good, bad := hex.EncodeToString(sum[:]), hex.EncodeToString(make([]byte, 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good-digest":
			w.Header().Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(sum[:])+":")
		case "/bad-digest":
			w.Header().Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(make([]byte, 32))+":")
		case "/encoded":
			w.Header().Set("Content-Encoding", "identity-but-not") // not decoded here; the digest is skipped
			w.Header().Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(make([]byte, 32))+":")
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	dir := t.TempDir()
	for i, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"download, right", []string{"download", "--checksum", "sha-256=" + good}, 0},
		{"download, wrong", []string{"download", "--checksum", "sha-256=" + bad}, 32},
		{"stdout, wrong", []string{"--checksum", "sha-256=" + bad}, 32},
		{"Content-Digest right", []string{"download", "--path", "/good-digest"}, 0},
		{"Content-Digest wrong", []string{"download", "--path", "/bad-digest"}, 32},
		{"Content-Digest wrong, stdout", []string{"--path", "/bad-digest"}, 32},
		{"Content-Digest of an encoded body", []string{"--no-compressed", "--path", "/encoded"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, args := srv.URL+"/f", []string{"-q"}
			for j := 0; j < len(tc.args); j++ {
				if tc.args[j] == "--path" {
					url, j = srv.URL+tc.args[j+1], j+1
					continue
				}
				args = append(args, tc.args[j])
			}
			if len(args) > 1 && args[1] == "download" {
				args = append([]string{"download", "-q", "-o", filepath.Join(dir, fmt.Sprint(i))}, args[2:]...)
			}
			if code := runGhola(t, append(args, url)...); code != tc.want {
				t.Errorf("exit %d, want %d", code, tc.want)
			}
		})
	}
	if b, err := os.ReadFile(filepath.Join(dir, "1")); err != nil || string(b) != body {
		t.Errorf("the file of the wrong checksum: %q, %v; it must stay", b, err)
	}
}
