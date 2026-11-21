package mirror

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// convertAll changes the links of each saved page and style sheet to
// work offline, as wget -k does: a link to a saved file becomes a
// relative path to it, and a relative link to a file that was not saved
// becomes its full URL. With --backup-converted the original stays as
// FILE.orig.
func (m *Mirror) convertAll() error {
	var last error
	for raw, local := range m.saved {
		kind := pageKind("", local)
		if kind == "" {
			continue
		}
		body, err := os.ReadFile(local)
		if err != nil {
			last = err
			continue
		}
		base, _ := url.Parse(raw)
		var out []byte
		if kind == "css" {
			out = m.convertCSS(base, local, body)
		} else {
			out = m.convertHTML(base, local, body)
		}
		if bytes.Equal(out, body) {
			continue
		}
		if m.Opts.Backup {
			if err := os.WriteFile(local+".orig", body, 0o644); err != nil {
				last = err
				continue
			}
		}
		if err := os.WriteFile(local, out, 0o644); err != nil {
			last = fmt.Errorf("--convert-links %s: %w", local, err)
		}
	}
	return last
}

// target is the new form of one link of the file at local.
func (m *Mirror) target(base *url.URL, local, raw string) string {
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "mailto:") {
		return raw
	}
	u, err := base.Parse(raw)
	if err != nil {
		return raw
	}
	frag := u.Fragment
	u.Fragment = ""
	if saved, ok := m.saved[u.String()]; ok {
		rel, err := filepath.Rel(filepath.Dir(local), saved)
		if err == nil {
			rel = filepath.ToSlash(rel)
			if frag != "" {
				rel += "#" + frag
			}
			return (&url.URL{Path: rel}).String()
		}
	}
	u.Fragment = frag
	return u.String() // not saved: the full URL still works online
}

// convertHTML rewrites the link attributes; every other byte stays.
func (m *Mirror) convertHTML(base *url.URL, local string, body []byte) []byte {
	z := html.NewTokenizer(bytes.NewReader(body))
	var out bytes.Buffer
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		tok := z.Token()
		if m.convertTag(&tok, base, local) {
			out.WriteString(tok.String())
		} else {
			out.Write(raw)
		}
	}
}

// convertTag rewrites the link attribute of one tag. It reports whether
// the tag changed.
func (m *Mirror) convertTag(tok *html.Token, base *url.URL, local string) bool {
	want := "href" // <link>
	if la, ok := linkAttrs[tok.Data]; ok {
		want = la.attr
	} else if tok.Data != "link" {
		return false
	}
	changed := false
	for i, a := range tok.Attr {
		if a.Key != want {
			continue
		}
		if t := m.target(base, local, a.Val); t != a.Val {
			tok.Attr[i].Val, changed = t, true
		}
	}
	return changed
}

// convertCSS rewrites url() and @import.
func (m *Mirror) convertCSS(base *url.URL, local string, body []byte) []byte {
	return cssURL.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := cssURL.FindSubmatch(match)
		raw := string(sub[1])
		if raw == "" {
			raw = string(sub[2])
		}
		t := m.target(base, local, raw)
		return bytes.Replace(match, []byte(raw), []byte(t), 1)
	})
}
