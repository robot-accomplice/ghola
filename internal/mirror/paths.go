package mirror

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
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
		out = append(out, safeName(d, names))
	}
	return filepath.Join(out...)
}

// windowsForbidden are the characters a Windows file name may not hold.
const windowsForbidden = `<>:"\|?*`

// safeName escapes the characters that --file-names forbids: unix forbids
// only "/", windows also <>:"\|?* and control characters, and ascii also
// every byte above 127. The default is the system's rule.
func safeName(name, rule string) string {
	if rule == "" {
		rule = "unix"
		if runtime.GOOS == "windows" {
			rule = "windows"
		}
	}
	if name == "." || name == ".." {
		return "%2E" + name[1:] // a name may not climb out of the copy
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		bad := c == '/' || c == 0
		switch rule {
		case "windows":
			bad = bad || c < 0x20 || strings.IndexByte(windowsForbidden, c) >= 0
		case "ascii":
			bad = bad || c < 0x20 || c > 0x7e
		}
		if bad {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
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
