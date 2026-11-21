// Package mirror copies a site, as wget -r and -m do (v1 spec, phase 4):
// it follows the links of each page within the scope rules, and saves
// each file under a local path that copies the URL.
package mirror

import (
	"net/url"
	"path"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
)

// scope decides which URLs the mirror follows.
type scope struct {
	opts      config.MirrorOptions
	startHost string
	startDir  string // the folder of the start URL, with a final /
}

func newScope(start *url.URL, opts config.MirrorOptions) *scope {
	dir := start.Path
	if !strings.HasSuffix(dir, "/") {
		dir = path.Dir(dir) + "/"
	}
	if dir == "./" || dir == "//" {
		dir = "/"
	}
	return &scope{opts: opts, startHost: strings.ToLower(start.Hostname()), startDir: dir}
}

// allows reports whether u is in scope. A page requisite (an image or a
// style of a page in scope) passes the folder rules, as in wget -p, but
// not the host and regex rules.
func (s *scope) allows(u *url.URL, requisite bool) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if !s.hostAllowed(strings.ToLower(u.Hostname())) {
		return false
	}
	full := u.String()
	if s.opts.AcceptRegex != nil && !s.opts.AcceptRegex.MatchString(full) {
		return false
	}
	if s.opts.RejectRegex != nil && s.opts.RejectRegex.MatchString(full) {
		return false
	}
	if requisite {
		return true
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	if !s.opts.Parent && strings.EqualFold(u.Hostname(), s.startHost) && !strings.HasPrefix(p, s.startDir) {
		return false
	}
	if len(s.opts.IncludeDirs) > 0 && !underAny(p, s.opts.IncludeDirs) {
		return false
	}
	return !underAny(p, s.opts.ExcludeDirs)
}

// keeps reports whether the file is saved: --accept and --reject match
// the file name. A page that does not match is still read for its links,
// as in wget.
func (s *scope) keeps(u *url.URL) bool {
	name := path.Base(u.Path)
	if strings.HasSuffix(u.Path, "/") || u.Path == "" {
		name = "index.html"
	}
	if len(s.opts.Accept) > 0 && !matchAny(name, s.opts.Accept) {
		return false
	}
	return !matchAny(name, s.opts.Reject)
}

func (s *scope) hostAllowed(host string) bool {
	if len(s.opts.Hosts) == 0 {
		return host == s.startHost
	}
	for _, h := range s.opts.Hosts {
		h = strings.ToLower(strings.TrimPrefix(h, "."))
		if h == "*" || host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// underAny reports a path inside one of the folders. A folder may hold
// a * glob, as wget -I does.
func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		d = "/" + strings.Trim(d, "/")
		for dir := path.Dir(p + "x"); ; dir = path.Dir(dir) {
			if ok, _ := path.Match(d, dir); ok {
				return true
			}
			if dir == "/" || dir == "." {
				break
			}
		}
	}
	return false
}

func matchAny(name string, globs []string) bool {
	for _, g := range globs {
		if !strings.ContainsAny(g, "*?[") {
			// wget -A takes a suffix too: -A pdf keeps a.pdf.
			if strings.HasSuffix(strings.ToLower(name), "."+strings.ToLower(strings.TrimPrefix(g, "."))) || name == g {
				return true
			}
			continue
		}
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}
