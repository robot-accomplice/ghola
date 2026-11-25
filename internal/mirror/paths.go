package mirror

import (
	"github.com/robot-accomplice/ghola/internal/localname"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// localPath is where the mirror saves u under root, as wget does:
// --layout full is host/path, no-host is path (wget -nH), flat is the
// file name alone (wget -nd). --cut-dirs drops leading folders. A folder
// URL saves as index.html, and a query stays in the name. The characters
// that --file-names forbids become %XX.
func localPath(root string, u *url.URL, layout string, cutDirs int, names string) string {
	p := u.Path
	if p == "" || strings.HasSuffix(p, "/") {
		p += "index.html"
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	file := parts[len(parts)-1]
	dirs := parts[:len(parts)-1]
	if cutDirs >= len(dirs) {
		dirs = nil
	} else {
		dirs = dirs[cutDirs:]
	}
	if u.RawQuery != "" {
		file += "?" + u.RawQuery
	}
	switch layout {
	case "flat":
		dirs = nil
	case "no-host":
	default:
		host := u.Hostname()
		if port := u.Port(); port != "" {
			host += "+" + port // wget writes host:port; ":" is not a file name character everywhere
		}
		dirs = append([]string{host}, dirs...)
	}
	out := make([]string, 0, len(dirs)+2)
	out = append(out, root)
	for _, d := range append(dirs, file) {
		out = append(out, localname.Escape(d, fileNameRule(names)))
	}
	return filepath.Join(out...)
}

// fileNameRule is the --file-names rule, or the system's rule when none
// is set.
func fileNameRule(rule string) string {
	if rule == "" {
		return localname.SystemRule()
	}
	return rule
}

// withExtension adds .html or .css when the content type needs it and the
// name does not have it (--adjust-extension, wget -E).
func withExtension(file, contentType string) string {
	ct := strings.ToLower(contentType)
	ext := strings.ToLower(path.Ext(file))
	switch {
	case strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml"):
		if ext != ".html" && ext != ".htm" {
			return file + ".html"
		}
	case strings.HasPrefix(ct, "text/css"):
		if ext != ".css" {
			return file + ".css"
		}
	}
	return file
}
