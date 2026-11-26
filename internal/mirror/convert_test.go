package mirror

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// --convert-links makes the copy work offline: a link to a saved file,
// with its #fragment, becomes a relative link that a browser opens at the
// fragment, and the image candidates of srcset and the url() links of a
// style attribute and a <style> element point to the saved files too. A
// file that was not saved keeps its full URL.
func TestConvertHTML_KeepsFragmentsAndConvertsSrcsetAndStyle(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "d", "p.html")
	m := &Mirror{saved: map[string]string{
		"http://h/d/p.html": sub,
		"http://h/b.html":   filepath.Join(root, "b.html"),
		"http://h/s.png":    filepath.Join(root, "s.png"),
		"http://h/l.png":    filepath.Join(root, "l.png"),
		"http://h/bg.png":   filepath.Join(root, "bg.png"),
	}}
	base, _ := url.Parse("http://h/d/p.html")
	page := `<a href="/b.html#part">b</a>` +
		`<img srcset="/s.png 1x, /l.png 2x, /gone.png 3x">` +
		`<div style="background: url('/bg.png')">x</div>` +
		`<style>p { background: url(/bg.png) }</style>`
	got := string(m.convertHTML(base, sub, []byte(page)))
	for _, want := range []string{
		`href="../b.html#part"`,
		`srcset="../s.png 1x, ../l.png 2x, http://h/gone.png 3x"`,
		`style="background: url(&#39;../bg.png&#39;)"`,
		`<style>p { background: url(../bg.png) }</style>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in\n%s", want, got)
		}
	}
}

// The url() links of a <style> element are requisites of the page, so
// the mirror saves them and --convert-links can point to them.
func TestPageLinks_ReadsTheStyleElement(t *testing.T) {
	base, _ := url.Parse("http://h/")
	links, _ := pageLinks(base, []byte(`<style>p { background: url(bg.png) }</style><p>url(text.png)</p>`))
	if len(links) != 1 || links[0].url.String() != "http://h/bg.png" || !links[0].requisite {
		t.Errorf("links: %+v", links)
	}
}
