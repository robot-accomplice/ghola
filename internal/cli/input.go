package cli

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
// lines (the default), html, sitemap or metalink.
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
	case "metalink":
		return inputMetalink(r)
	}
	return nil, fmt.Errorf("--input-format %q: use lines, html, sitemap or metalink", format)
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
			// Several URIs on one line are mirrors of one file, as in aria2.
			uris := strings.Split(trimmed, "\t")
			entries = append(entries, inputEntry{url: uris[0]})
			var mirrors []string
			for _, u := range uris[1:] {
				mirrors = append(mirrors, "--source", strings.TrimSpace(u))
			}
			opts = append(opts, nil)
			entries[len(entries)-1].args = mirrors
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
		entries[i].args = append(entries[i].args, args...)
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

// metalinkFile is one file of a Metalink: v4 (RFC 5854) puts the URLs and
// hashes under <file>, v3 puts them under <resources> and <verification>.
type metalinkFile struct {
	Name   string         `xml:"name,attr"`
	URLs   []metalinkURL  `xml:"url"`
	Hashes []metalinkHash `xml:"hash"`
	V3URLs []metalinkURL  `xml:"resources>url"`
	V3Hash []metalinkHash `xml:"verification>hash"`
}

type metalinkURL struct {
	Priority   int    `xml:"priority,attr"`   // v4: 1 is the best
	Preference int    `xml:"preference,attr"` // v3: 100 is the best
	URL        string `xml:",chardata"`
}

type metalinkHash struct {
	Type string `xml:"type,attr"`
	Sum  string `xml:",chardata"`
}

// metalinkAlgos map the hash names of Metalink to --checksum names, the
// strongest first.
var metalinkAlgos = []struct{ names, algo string }{
	{"sha-512 sha512", "sha-512"}, {"sha-384 sha384", "sha-384"}, {"sha-256 sha256", "sha-256"},
	{"sha-1 sha1", "sha-1"}, {"md5", "md5"},
}

// inputMetalink reads a Metalink (v4 or v3): each file is one entry. The
// best URL fetches it, the others are its --source mirrors, the strongest
// file hash is its --checksum, and its name is its -o, inside the run's
// -o DIR/. Piece hashes and signatures are not checked yet: the file hash
// checks the same bytes, piece repair needs a refetch of each piece, and
// a signature needs an OpenPGP keyring.
func inputMetalink(r io.Reader) ([]inputEntry, error) {
	var doc struct {
		Files []metalinkFile `xml:"file"`
		V3    []metalinkFile `xml:"files>file"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("--input metalink: %w", err)
	}
	var entries []inputEntry
	for _, f := range append(doc.Files, doc.V3...) {
		urls := append(f.URLs, f.V3URLs...)
		sort.SliceStable(urls, func(a, b int) bool { return urlRank(urls[a]) < urlRank(urls[b]) })
		if len(urls) == 0 {
			continue
		}
		var args []string
		for _, u := range urls[1:] {
			args = append(args, "--source", strings.TrimSpace(u.URL))
		}
		if sum := strongestHash(append(f.Hashes, f.V3Hash...)); sum != "" {
			args = append(args, "--checksum", sum)
		}
		if name := filepath.Base(f.Name); f.Name != "" && name == f.Name {
			args = append(args, "-o", name) // a name with a folder part is not taken: it could leave the folder
		}
		entries = append(entries, inputEntry{url: strings.TrimSpace(urls[0].URL), args: args})
	}
	return entries, nil
}

// urlRank orders the URLs, the best first: v4 priority 1 first, v3 the
// highest preference first, and a URL without either last.
func urlRank(u metalinkURL) int {
	switch {
	case u.Priority > 0:
		return u.Priority
	case u.Preference > 0:
		return 1000 - u.Preference
	}
	return 1 << 20
}

func strongestHash(hashes []metalinkHash) string {
	for _, a := range metalinkAlgos {
		for _, h := range hashes {
			if strings.Contains(" "+a.names+" ", " "+strings.ToLower(h.Type)+" ") {
				return a.algo + "=" + strings.ToLower(strings.TrimSpace(h.Sum))
			}
		}
	}
	return ""
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
		if len(e.args) > 0 && c.Job != "" {
			e.args = append([]string{c.Job}, e.args...) // so the download options of the entry parse
		}
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
