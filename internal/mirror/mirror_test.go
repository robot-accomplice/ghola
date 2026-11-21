package mirror

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// The local path copies the URL as wget does, and a name cannot climb out
// of the copy or hold a character that the file system refuses.
func TestLocalPath(t *testing.T) {
	for _, tc := range []struct {
		url, layout string
		cut         int
		names, want string
	}{
		{"http://h/a/b/c.html", "full", 0, "unix", "h/a/b/c.html"},
		{"http://h:8080/a/", "full", 0, "unix", "h+8080/a/index.html"},
		{"http://h/a/b/c.html", "no-host", 1, "unix", "b/c.html"},
		{"http://h/a/b/c.html", "flat", 0, "unix", "c.html"},
		{"http://h/s?q=1", "no-host", 0, "unix", "s?q=1"},
		{"http://h/s?q=1", "no-host", 0, "windows", "s%3Fq=1"},
		{"http://h/caf%C3%A9", "no-host", 0, "ascii", "caf%C3%A9"},
	} {
		got := filepath.ToSlash(localPath("", mustURL(t, tc.url), tc.layout, tc.cut, tc.names))
		if got != tc.want {
			t.Errorf("%s (%s, cut %d, %s): %s, want %s", tc.url, tc.layout, tc.cut, tc.names, got, tc.want)
		}
	}
	if got := safeName("..", "unix"); got == ".." {
		t.Error(`".." stays a name that climbs out of the copy`)
	}
}

// The scope keeps links on the start host and below the start folder,
// with the folder and name rules of wget.
func TestScope(t *testing.T) {
	s := newScope(mustURL(t, "http://h/docs/"), config.MirrorOptions{
		ExcludeDirs: []string{"/docs/old"}, Accept: []string{"html", "*.pdf"},
	})
	for raw, want := range map[string]bool{
		"http://h/docs/a.html": true, "http://h/other.html": false, "http://x/docs/a.html": false,
		"http://h/docs/old/a.html": false, "ftp://h/docs/a": false,
	} {
		if got := s.allows(mustURL(t, raw), false); got != want {
			t.Errorf("allows(%s) = %v", raw, got)
		}
	}
	if !s.allows(mustURL(t, "http://h/img/x.png"), true) {
		t.Error("a requisite outside the start folder was refused")
	}
	for raw, want := range map[string]bool{"http://h/docs/a.html": true, "http://h/docs/b.pdf": true, "http://h/docs/c.zip": false} {
		if got := s.keeps(mustURL(t, raw)); got != want {
			t.Errorf("keeps(%s) = %v", raw, got)
		}
	}
	listed := newScope(mustURL(t, "http://h/"), config.MirrorOptions{Hosts: []string{"example.com"}})
	if !listed.allows(mustURL(t, "http://cdn.example.com/x"), false) || listed.allows(mustURL(t, "http://h/x"), false) {
		t.Error("--hosts: a subdomain of a listed host goes, an unlisted host does not")
	}
}

// robots.txt follows RFC 9309: the agent's own group first, the longest
// rule wins, Allow wins a tie, and * and $ match.
func TestRobots(t *testing.T) {
	r := parseRobots(strings.NewReader("User-agent: *\nDisallow: /\n\nUser-agent: ghola\nDisallow: /private/\nAllow: /private/ok\nDisallow: /*.gif$\n"))
	for p, want := range map[string]bool{
		"/": true, "/private/x": false, "/private/ok.html": true, "/a.gif": false, "/a.gif.txt": true,
	} {
		if got := r.allows(p); got != want {
			t.Errorf("allows(%s) = %v", p, got)
		}
	}
	if !parseRobots(strings.NewReader("")).allows("/x") {
		t.Error("an empty robots.txt refused a path")
	}
}

// --adjust-extension adds .html or .css only when the name lacks it.
func TestWithExtension(t *testing.T) {
	for in, want := range map[string]string{"a.php": "a.php.html", "b.html": "b.html", "s": "s.css"} {
		ct := "text/html"
		if in == "s" {
			ct = "text/css"
		}
		if got := withExtension(in, ct); got != want {
			t.Errorf("withExtension(%s) = %s, want %s", in, got, want)
		}
	}
}
