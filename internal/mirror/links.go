package mirror

import (
	"bytes"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// link is one link of a page.
type link struct {
	url       *url.URL
	requisite bool // an image, a style or a script that the page needs
	nofollow  bool // rel=nofollow: --robots does not follow it
}

// linkAttrs are the attributes that hold links, by tag. requisite marks
// the ones that a page needs to show, as wget -p takes them.
var linkAttrs = map[string]struct {
	attr      string
	requisite bool
}{
	"a": {"href", false}, "area": {"href", false}, "iframe": {"src", true}, "frame": {"src", true},
	"img": {"src", true}, "script": {"src", true}, "source": {"src", true}, "video": {"src", true},
	"audio": {"src", true}, "embed": {"src", true}, "input": {"src", true},
}

// cssURL finds url(...) and @import "..." in CSS.
var cssURL = regexp.MustCompile(`url\(\s*['"]?([^'")\s]+)['"]?\s*\)|@import\s+['"]([^'"]+)['"]`)

// pageLinks returns the links of an HTML page at base. nofollow is the
// robots meta tag of the page: with --robots, its <a> links are not
// followed, but its requisites are, as in wget.
func pageLinks(base *url.URL, body []byte) (links []link, nofollow bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	add := func(raw string, requisite bool) {
		if u, err := base.Parse(strings.TrimSpace(raw)); err == nil && raw != "" && !strings.HasPrefix(raw, "#") {
			u.Fragment = ""
			links = append(links, link{url: u, requisite: requisite})
		}
	}
	addNofollow := func(raw string) {
		before := len(links)
		add(raw, false)
		if len(links) > before {
			links[before].nofollow = true
		}
	}
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return links, nofollow
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, _ := z.TagName()
		attrs := map[string]string{}
		for more := true; more; {
			var k, v []byte
			k, v, more = z.TagAttr()
			attrs[string(k)] = string(v)
		}
		tag := string(name)
		switch tag {
		case "base":
			if u, err := base.Parse(attrs["href"]); err == nil && attrs["href"] != "" {
				base = u
			}
		case "meta":
			if strings.EqualFold(attrs["name"], "robots") && strings.Contains(strings.ToLower(attrs["content"]), "nofollow") {
				nofollow = true
			}
		case "link":
			rel := strings.ToLower(attrs["rel"])
			add(attrs["href"], strings.Contains(rel, "stylesheet") || strings.Contains(rel, "icon"))
		}
		if la, ok := linkAttrs[tag]; ok {
			if tag == "a" && strings.Contains(strings.ToLower(attrs["rel"]), "nofollow") {
				addNofollow(attrs[la.attr])
				continue
			}
			add(attrs[la.attr], la.requisite)
		}
		for _, src := range strings.Split(attrs["srcset"], ",") {
			if f := strings.Fields(src); len(f) > 0 {
				add(f[0], true)
			}
		}
		if style := attrs["style"]; style != "" {
			links = append(links, cssLinks(base, []byte(style))...)
		}
	}
}

// cssLinks returns the url() and @import links of a style sheet. They are
// requisites.
func cssLinks(base *url.URL, body []byte) []link {
	var links []link
	for _, m := range cssURL.FindAllSubmatch(body, -1) {
		raw := string(m[1])
		if raw == "" {
			raw = string(m[2])
		}
		if strings.HasPrefix(raw, "data:") {
			continue
		}
		if u, err := base.Parse(raw); err == nil {
			u.Fragment = ""
			links = append(links, link{url: u, requisite: true})
		}
	}
	return links
}

// robotsRules are the Allow and Disallow lines of robots.txt that apply
// to ghola: its own group, else the * group.
type robotsRules struct {
	allow, disallow []string
}

// parseRobots reads robots.txt (RFC 9309) for the agent "ghola".
func parseRobots(r io.Reader) robotsRules {
	b, _ := io.ReadAll(io.LimitReader(r, 500<<10)) // RFC 9309 asks to read at least 500 KiB
	groups := map[string]*robotsRules{}
	var current []string
	inRules := false
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if inRules {
				current, inRules = nil, false
			}
			agent := strings.ToLower(v)
			current = append(current, agent)
			if groups[agent] == nil {
				groups[agent] = &robotsRules{}
			}
		case "allow", "disallow":
			inRules = true
			for _, a := range current {
				if k == "allow" {
					groups[a].allow = append(groups[a].allow, v)
				} else if v != "" {
					groups[a].disallow = append(groups[a].disallow, v)
				}
			}
		}
	}
	if g := groups["ghola"]; g != nil {
		return *g
	}
	if g := groups["*"]; g != nil {
		return *g
	}
	return robotsRules{}
}

// allows applies the longest matching rule; Allow wins a tie (RFC 9309).
func (r robotsRules) allows(p string) bool {
	best, allowed := -1, true
	for _, rule := range r.allow {
		if robotsMatch(rule, p) && len(rule) >= best {
			best, allowed = len(rule), true
		}
	}
	for _, rule := range r.disallow {
		if robotsMatch(rule, p) && len(rule) > best {
			best, allowed = len(rule), false
		}
	}
	return allowed
}

// robotsMatch matches a rule with * and a final $, as RFC 9309 has them.
func robotsMatch(rule, p string) bool {
	anchored := strings.HasSuffix(rule, "$")
	parts := strings.Split(strings.TrimSuffix(rule, "$"), "*")
	if !strings.HasPrefix(p, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, part := range parts[1:] {
		i := strings.Index(p[pos:], part)
		if i < 0 {
			return false
		}
		pos += i + len(part)
	}
	// With $, the last part must end the path, not only appear in it; a
	// rule without * must then be the whole path.
	if anchored && len(parts) == 1 {
		return p == parts[0]
	}
	return !anchored || strings.HasSuffix(p, parts[len(parts)-1])
}
