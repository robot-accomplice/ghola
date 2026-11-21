package main

import (
	"crypto/md5" //nolint:gosec // the test server checks RFC 2617 Digest, which is MD5
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// digestServer asks for Digest and accepts only the right answer for
// user "u", password "p". It counts the requests that carried Basic.
func digestServer(t *testing.T, basicSeen *atomic.Int32) *httptest.Server {
	t.Helper()
	const realm, nonce = "test", "n0nce"
	md5hex := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) } //nolint:gosec
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Basic ") {
			basicSeen.Add(1)
		}
		if p := fields(strings.TrimPrefix(h, "Digest ")); strings.HasPrefix(h, "Digest ") {
			ha1, ha2 := md5hex("u:"+realm+":p"), md5hex(r.Method+":"+p["uri"])
			if p["response"] == md5hex(strings.Join([]string{ha1, nonce, p["nc"], p["cnonce"], p["qop"], ha2}, ":")) {
				fmt.Fprint(w, "secret")
				return
			}
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm=%q, nonce=%q, qop="auth"`, realm, nonce))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// fields reads the name="value" pairs of a Digest header, for the test.
func fields(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ", ") {
		k, v, _ := strings.Cut(part, "=")
		out[k] = strings.Trim(v, `"`)
	}
	return out
}

// --auth digest answers the server's challenge on stdout and on -o, and
// never sends the password in Basic form, which a Digest server does not
// expect to see in clear text. A wrong password exits 22.
func TestExecute_AuthDigest(t *testing.T) {
	var basic atomic.Int32
	ts := digestServer(t, &basic)
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"stdout", []string{"--auth", "digest", "-u", "u:p", ts.URL + "/a?b=1"}, 0},
		{"-o", []string{"--auth", "digest", "-u", "u:p", "-o", filepath.Join(dir, "f"), ts.URL + "/a"}, 0},
		{"download", []string{"download", "--auth", "digest", "-u", "u:p", "-o", filepath.Join(dir, "g"), ts.URL + "/a"}, 0},
		{"wrong password", []string{"--auth", "digest", "-u", "u:x", ts.URL}, 22},
		{"basic only", []string{"-u", "u:p", ts.URL}, 22},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runGhola(t, quiet(tc.args)...); got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
	}
	for _, f := range []string{"f", "g"} {
		if b, _ := os.ReadFile(filepath.Join(dir, f)); string(b) != "secret" {
			t.Errorf("%s = %q", f, b)
		}
	}
	if n := basic.Load(); n != 1 {
		t.Errorf("%d requests carried Basic; want 1, from the run without --auth", n)
	}
}

// --auth any answers a Basic-only server with Basic after its challenge.
func TestExecute_AuthAnyBasic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "u" && p == "p" {
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	if got := runGhola(t, quiet([]string{"--auth", "any", "-u", "u:p", ts.URL})...); got != 0 {
		t.Errorf("exit %d", got)
	}
}

// --auth-aws-sigv4 signs the request with -u instead of sending Basic,
// and a host that names no region is an argument error at once.
func TestExecute_AuthSigV4(t *testing.T) {
	var got atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization") + "|" + r.Header.Get("X-Amz-Content-Sha256"))
	}))
	defer ts.Close()
	if code := runGhola(t, "-q", "--auth-aws-sigv4", "aws:amz:us-east-1:s3", "-u", "AK:SK", ts.URL+"/b/k"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if s, _ := got.Load().(string); !strings.HasPrefix(s, "AWS4-HMAC-SHA256 Credential=AK/") || !strings.HasSuffix(s, "|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
		t.Errorf("Authorization|payload hash = %q", s)
	}
	if code := runGhola(t, "-q", "--auth-aws-sigv4", "aws", "-u", "AK:SK", ts.URL); code != 2 {
		t.Errorf("a host without a region: exit %d, want 2", code)
	}
}
