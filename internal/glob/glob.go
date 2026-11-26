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
	"errors"
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

// MaxURLs is the most URLs that the globs of one command line give.
// ghola builds every URL before the first request, so a larger glob is
// refused at once, not after it has filled the memory.
const MaxURLs = 100_000

// ErrTooMany is the error of a glob that gives more than MaxURLs URLs.
var ErrTooMany = fmt.Errorf("the URL globs give more than %d URLs", MaxURLs)

// errBadRange marks a range that cannot be read; parseGlob adds the
// position.
var errBadRange = errors.New(msgBadRange)

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
	for i := 0; i < len(url); i++ {
		c := url[i]
		switch {
		case c == '\\' && i+1 < len(url) && strings.IndexByte("{}[]", url[i+1]) >= 0:
			i++
			text.WriteByte(url[i])
		case c == '{' || c == '[':
			values, end, literal, err := parseGlob(url, i)
			if err != nil {
				return nil, err
			}
			if literal {
				text.WriteString(url[i : end+1])
			} else {
				p.addText(&text)
				p.parts = append(p.parts, part{values: values, glob: true})
			}
			i = end
		case c == '}' || c == ']':
			return nil, &Error{msgUnmatchedClose, i + 1}
		default:
			text.WriteByte(c)
		}
	}
	p.addText(&text)
	if p.Count() > MaxURLs {
		return nil, ErrTooMany
	}
	return p, nil
}

// addText adds the fixed text so far as a part.
func (p *Pattern) addText(text *strings.Builder) {
	if text.Len() > 0 {
		p.parts = append(p.parts, part{values: []string{text.String()}})
		text.Reset()
	}
}

// parseGlob reads the {list} or [range] that starts at url[start]. It
// returns the values and the index of the closing character. literal is
// true for an IPv6 address in brackets, which is not a range.
func parseGlob(url string, start int) (values []string, end int, literal bool, err error) {
	if url[start] == '{' {
		values, end, err = parseList(url, start)
		return values, end, false, err
	}
	end = strings.IndexByte(url[start:], ']')
	if end < 0 {
		return nil, 0, false, &Error{msgBadRange, len(url) + 1}
	}
	end += start
	inner := url[start+1 : end]
	if isIPv6(inner) {
		return nil, end, true, nil
	}
	values, err = parseRange(inner)
	if errors.Is(err, errBadRange) {
		return nil, 0, false, &Error{msgBadRange, end + 2}
	}
	if err != nil {
		return nil, 0, false, err
	}
	return values, end, false, nil
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
func parseRange(s string) ([]string, error) {
	spec, stepText, hasStep := strings.Cut(s, ":")
	from, to, ok := strings.Cut(spec, "-")
	if !ok || from == "" || to == "" {
		return nil, errBadRange
	}
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return nil, errBadRange
		}
		step = n
	}
	if a, b, ok := letters(from, to); ok {
		return letterRange(a, b, step), nil
	}
	return numberRange(from, to, step)
}

func letterRange(a, b byte, step int) []string {
	var out []string
	for c := int(a); c <= int(b); c += step {
		out = append(out, string(rune(c)))
	}
	return out
}

// numberRange keeps the width of a start with a leading zero.
func numberRange(from, to string, step int) ([]string, error) {
	a, errA := strconv.Atoi(from)
	b, errB := strconv.Atoi(to)
	if errA != nil || errB != nil || a < 0 || b < a {
		return nil, errBadRange
	}
	if (b-a)/step >= MaxURLs {
		return nil, ErrTooMany
	}
	width := 0
	if len(from) > 1 && from[0] == '0' {
		width = len(from)
	}
	var out []string
	for n := a; n <= b; n += step {
		out = append(out, fmt.Sprintf("%0*d", width, n))
	}
	return out, nil
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

// Count is the number of URLs that the pattern gives, or MaxURLs+1 when
// it gives more.
func (p *Pattern) Count() int {
	n := 1
	for _, pt := range p.parts {
		if n *= len(pt.values); n > MaxURLs {
			return MaxURLs + 1
		}
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
