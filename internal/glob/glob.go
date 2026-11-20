// Package glob expands curl's URL globs: {a,b,c} lists and [1-100],
// [001-100], [a-z] and [1-100:10] ranges (plan phase 2, item 10). The rules
// and the error messages come from curl 8.18.0, checked on 2026-11-20:
//
//   - The rightmost glob changes fastest.
//   - A range keeps the width of a start with a leading zero: [08-10] gives
//     08, 09, 10.
//   - A step works for letters too: [a-e:2] gives a, c, e.
//   - \{ \} \[ \] are literal characters; the backslash is dropped.
//   - A list may have an empty element: {a,} gives "a" and "".
//   - A bracket that holds an IPv6 address, as in http://[::1]:8080/, is
//     not a range.
//
// An error says where it is, as curl does: "bad range in URL position 16".
// curl exits 3 for these errors.
package glob

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Error is a malformed glob, with curl's message and 1-based position.
type Error struct {
	Msg string
	Pos int
}

func (e *Error) Error() string { return fmt.Sprintf("%s in URL position %d", e.Msg, e.Pos) }

// The messages of curl.
const (
	msgUnmatchedBrace = "unmatched brace"
	msgBadRange       = "bad range"
	msgNestedBrace    = "nested brace"
	msgUnmatchedClose = "unmatched close brace/bracket"
)

// Pattern is a parsed URL glob.
type Pattern struct {
	parts []part
}

// part is fixed text (one value) or a glob (several values).
type part struct {
	values []string
	glob   bool
}

// Parse parses a URL that may hold globs.
func Parse(url string) (*Pattern, error) {
	p := &Pattern{}
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			p.parts = append(p.parts, part{values: []string{text.String()}})
			text.Reset()
		}
	}
	for i := 0; i < len(url); i++ {
		c := url[i]
		switch {
		case c == '\\' && i+1 < len(url) && strings.IndexByte("{}[]", url[i+1]) >= 0:
			i++
			text.WriteByte(url[i])
		case c == '{':
			values, end, err := parseList(url, i)
			if err != nil {
				return nil, err
			}
			flush()
			p.parts = append(p.parts, part{values: values, glob: true})
			i = end
		case c == '[':
			end := strings.IndexByte(url[i:], ']')
			if end < 0 {
				return nil, &Error{msgBadRange, len(url) + 1}
			}
			end += i
			inner := url[i+1 : end]
			if isIPv6(inner) {
				text.WriteString(url[i : end+1])
				i = end
				continue
			}
			values, ok := parseRange(inner)
			if !ok {
				return nil, &Error{msgBadRange, end + 2}
			}
			flush()
			p.parts = append(p.parts, part{values: values, glob: true})
			i = end
		case c == '}' || c == ']':
			return nil, &Error{msgUnmatchedClose, i + 1}
		default:
			text.WriteByte(c)
		}
	}
	flush()
	return p, nil
}

// parseList reads the {a,b,c} list that starts at url[start]. It returns
// the values and the index of the closing brace.
func parseList(url string, start int) ([]string, int, error) {
	var values []string
	var cur strings.Builder
	for i := start + 1; i < len(url); i++ {
		switch c := url[i]; {
		case c == '\\' && i+1 < len(url) && strings.IndexByte("{}[]", url[i+1]) >= 0:
			i++
			cur.WriteByte(url[i])
		case c == '{':
			return nil, 0, &Error{msgNestedBrace, i + 1}
		case c == ',':
			values = append(values, cur.String())
			cur.Reset()
		case c == '}':
			return append(values, cur.String()), i, nil
		default:
			cur.WriteByte(c)
		}
	}
	return nil, 0, &Error{msgUnmatchedBrace, start + 1}
}

// parseRange reads the inside of [a-b] or [a-b:step]: numbers, or letters
// of the same case.
func parseRange(s string) ([]string, bool) {
	spec, stepText, hasStep := strings.Cut(s, ":")
	from, to, ok := strings.Cut(spec, "-")
	if !ok || from == "" || to == "" {
		return nil, false
	}
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return nil, false
		}
		step = n
	}
	if a, b, ok := letters(from, to); ok {
		var out []string
		for c := a; c <= b; c += byte(step) {
			out = append(out, string(c))
			if int(c)+step > 255 {
				break
			}
		}
		return out, true
	}
	a, errA := strconv.Atoi(from)
	b, errB := strconv.Atoi(to)
	if errA != nil || errB != nil || a < 0 || b < a {
		return nil, false
	}
	width := 0
	if len(from) > 1 && from[0] == '0' {
		width = len(from)
	}
	var out []string
	for n := a; n <= b; n += step {
		out = append(out, fmt.Sprintf("%0*d", width, n))
	}
	return out, true
}

// letters reports a letter range of one case, from <= to.
func letters(from, to string) (byte, byte, bool) {
	if len(from) != 1 || len(to) != 1 {
		return 0, 0, false
	}
	a, b := from[0], to[0]
	lower := 'a' <= a && a <= 'z' && 'a' <= b && b <= 'z'
	upper := 'A' <= a && a <= 'Z' && 'A' <= b && b <= 'Z'
	return a, b, (lower || upper) && a <= b
}

// isIPv6 reports an IPv6 address in brackets, with an optional %zone.
func isIPv6(s string) bool {
	addr, _, _ := strings.Cut(s, "%")
	ip := net.ParseIP(addr)
	return ip != nil && strings.Contains(addr, ":")
}

// Count is the number of URLs that the pattern gives.
func (p *Pattern) Count() int {
	n := 1
	for _, pt := range p.parts {
		n *= len(pt.values)
	}
	return n
}

// Each calls fn for each URL, in curl's order: the rightmost glob changes
// fastest. values holds the value of each glob, for #1, #2 in output names.
// fn returns false to stop.
func (p *Pattern) Each(fn func(url string, values []string) bool) {
	idx := make([]int, len(p.parts))
	for {
		var url strings.Builder
		var values []string
		for i, pt := range p.parts {
			v := pt.values[idx[i]]
			url.WriteString(v)
			if pt.glob {
				values = append(values, v)
			}
		}
		if !fn(url.String(), values) {
			return
		}
		i := len(idx) - 1
		for ; i >= 0; i-- {
			if idx[i]++; idx[i] < len(p.parts[i].values) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return
		}
	}
}

// OutputName replaces #1, #2 ... in an -o name with the glob values, as
// curl does. A # that does not name a glob stays as it is.
func OutputName(name string, values []string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] != '#' {
			b.WriteByte(name[i])
			continue
		}
		j := i + 1
		for j < len(name) && '0' <= name[j] && name[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(name[i+1 : j])
		if err != nil || n < 1 || n > len(values) {
			b.WriteByte('#')
			continue
		}
		b.WriteString(values[n-1])
		i = j - 1
	}
	return b.String()
}
