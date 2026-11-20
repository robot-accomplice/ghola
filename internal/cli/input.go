package cli

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"

	"github.com/robot-accomplice/ghola/internal/config"
)

// inputEntry is one URL of an --input file, with its own options in
// native form.
type inputEntry struct {
	url  string
	args []string
}

// readInput reads the --input file (- is stdin) in the --input-format:
// lines (the default), html or sitemap.
func readInput(path, format string) ([]inputEntry, error) {
	r := io.Reader(os.Stdin)
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("--input: %w", err)
		}
		defer f.Close()
		r = f
	}
	switch format {
	case "", "lines":
		return inputLines(r)
	case "html":
		return inputHTML(r)
	case "sitemap":
		return inputSitemap(r)
	}
	return nil, fmt.Errorf("--input-format %s is not built yet: use lines, html or sitemap", format)
}

// inputLines reads aria2's input format: a URL on each line, then lines
// that start with a space or a tab hold that URL's options, as
// "name=value" with aria2's names (out, dir, header, checksum, ...). #
// starts a comment line.
func inputLines(r io.Reader) ([]inputEntry, error) {
	var entries []inputEntry
	var opts [][]string
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		case line[0] == ' ' || line[0] == '\t':
			if len(entries) == 0 {
				return nil, fmt.Errorf("--input line %d: an option comes before any URL", n)
			}
			opts[len(opts)-1] = append(opts[len(opts)-1], "--"+trimmed)
		default:
			uris := strings.Split(trimmed, "\t")
			if len(uris) > 1 {
				return nil, fmt.Errorf("--input line %d: several URIs of one file (mirrors) are not built yet", n)
			}
			entries = append(entries, inputEntry{url: uris[0]})
			opts = append(opts, nil)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("--input: %w", err)
	}
	for i := range entries {
		args, err := aria2Options(opts[i])
		if err != nil {
			return nil, fmt.Errorf("--input, the options of %s: %w", entries[i].url, err)
		}
		entries[i].args = args
	}
	return entries, nil
}

// aria2Options translates aria2 options to native ones, without aria2's
// defaults: the command line already holds the defaults.
func aria2Options(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	t := &translation{spec: aria2Spec}
	if err := t.run(args); err != nil {
		return nil, err
	}
	if len(t.tail) > 0 {
		return nil, fmt.Errorf("%q is not an option", t.tail[0])
	}
	if out := joinOutput(t.dir, t.file); out != "" {
		t.out = append(t.out, "-o", out)
	}
	return t.out, nil
}

// inputHTML takes the href of each <a> and the src of each <img>, as
// wget -F -i does. The file has no base URL, so a relative link is
// skipped; wget needs -B for it, which the v1 spec does not have.
func inputHTML(r io.Reader) ([]inputEntry, error) {
	var entries []inputEntry
	z := html.NewTokenizer(r)
	for {
		switch z.Next() {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				return entries, nil
			}
			return nil, fmt.Errorf("--input: %w", z.Err())
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			want := map[string]string{"a": "href", "img": "src"}[string(name)]
			for want != "" {
				k, v, more := z.TagAttr()
				if string(k) == want {
					if u, err := url.Parse(string(v)); err == nil && u.IsAbs() {
						entries = append(entries, inputEntry{url: u.String()})
					}
				}
				if !more {
					break
				}
			}
		}
	}
}

// inputSitemap takes each <loc> of a sitemap (sitemaps.org).
func inputSitemap(r io.Reader) ([]inputEntry, error) {
	var doc struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("--input sitemap: %w", err)
	}
	entries := make([]inputEntry, 0, len(doc.URLs))
	for _, u := range doc.URLs {
		if loc := strings.TrimSpace(u.Loc); loc != "" {
			entries = append(entries, inputEntry{url: loc})
		}
	}
	return entries, nil
}

// applyInput adds the URLs of --input after the URLs of the command line,
// and keeps the options of each, checked now, so a bad line stops the run
// before any transfer.
func applyInput(o *config.Options, c *Command) error {
	path := c.Last("input")
	if path == "" {
		return nil
	}
	entries, err := readInput(path, c.Last("input-format"))
	if err != nil {
		return err
	}
	if len(entries) == 0 && len(o.URLs) == 0 {
		return fmt.Errorf("--input %s holds no URL", path)
	}
	args := make([][]string, len(o.URLs), len(o.URLs)+len(entries))
	for _, e := range entries {
		if len(e.args) > 0 {
			if _, err := Parse(e.args); err != nil {
				return fmt.Errorf("--input, the options of %s: %w", e.url, err)
			}
		}
		o.URLs = append(o.URLs, config.NormalizeURL(e.url))
		args = append(args, e.args)
	}
	if o.URL == "" {
		o.URL = o.URLs[0]
	}
	o.Output.InputArgs = args
	return nil
}

// ApplyInputArgs sets the options of one --input URL on its copy of the
// options: each runs its setter, so it replaces the command line's value
// for that URL.
func ApplyInputArgs(o *config.Options, args []string) error {
	if len(args) == 0 {
		return nil
	}
	c, err := Parse(args)
	if err != nil {
		return err
	}
	dir := o.Output.Dir
	defer func() {
		// As in aria2, out= of an entry goes inside the run's -o DIR/.
		if f := o.Output.File; c.Has("output") && dir != "" && f != "" && !filepath.IsAbs(f) && filepath.Dir(f) == "." {
			o.Output.File = filepath.Join(dir, f)
		}
	}()
	for name := range c.Values {
		set, ok := setters[name]
		if !ok {
			return usagef("--%s in --input is not built yet", name)
		}
		if err := set(o, c.Last(name), c); err != nil {
			return err
		}
	}
	return nil
}
