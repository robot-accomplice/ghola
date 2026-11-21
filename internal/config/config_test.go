package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestURLEncodeData(t *testing.T) {
	got := URLEncodeData([]string{"q=a b", "x=1+2"})
	if got != "q=a+b&x=1%2B2" {
		t.Errorf("URLEncodeData = %q", got)
	}
}

func TestBuildFormBody_Fields(t *testing.T) {
	ct, body, err := BuildFormBody([]string{"name=ghola", "kind=cli"})
	if err != nil {
		t.Fatalf("BuildFormBody error: %v", err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(string(body), `name="name"`) || !strings.Contains(string(body), "ghola") {
		t.Errorf("body missing field: %s", body)
	}
}

func TestBuildFormBody_FileAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	content := []byte("hello from file")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	ct, body, err := BuildFormBody([]string{"upload=@" + path})
	if err != nil {
		t.Fatalf("BuildFormBody error: %v", err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Errorf("content-type = %q, want multipart/form-data prefix", ct)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, `name="upload"`) {
		t.Errorf("body missing filename Content-Disposition for field 'upload': %s", bodyStr)
	}
	if !strings.Contains(bodyStr, string(content)) {
		t.Errorf("body missing file content %q: %s", content, bodyStr)
	}
}

func TestBuildFormBody_MalformedEntry(t *testing.T) {
	_, _, err := BuildFormBody([]string{"noequalsign"})
	if err == nil {
		t.Fatal("expected error for malformed entry without '='")
	}
}

func TestShouldStream(t *testing.T) {
	cases := []struct {
		name string
		opts *Options
		want bool
	}{
		{"file get", &Options{Method: "GET", Output: OutputOptions{File: "x"}}, true},
		{"stdout", &Options{Method: "GET"}, false},
		{"jq forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", JQ: ".a"}}, false},
		{"snoop forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", Snoop: true}}, false},
		{"har forces buffer", &Options{Method: "GET", Output: OutputOptions{File: "x", HAR: "h.har"}}, false},
		{"post not streamed", &Options{Method: "POST", Output: OutputOptions{File: "x"}}, false},
	}
	for _, tc := range cases {
		if got := ShouldStream(tc.opts); got != tc.want {
			t.Errorf("%s: ShouldStream = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// curl --location-trusted follows redirects without -L. A user who gives it
// alone must not get the 3xx response instead of the target.
