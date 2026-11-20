package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	byName  = map[string]*Option{}
	byShort = map[byte]*Option{}
	// v0Index maps each v0 flag (-b, --backoff) to its mapping row.
	v0Index = map[string]*V0Flag{}
	// otherIndex maps a curl, wget or aria2 flag to the native options it
	// stands for, in each tool, for the "is a curl flag" message. One flag
	// can mean different things: -x is curl --proxy and aria2c --connections.
	otherIndex = map[string][]otherFlag{}
)

type otherFlag struct {
	tool string
	opt  *Option
}

var flagToken = regexp.MustCompile(`(^|[\s/(])(--?[A-Za-z0-9#][A-Za-z0-9.-]*)`)

func init() {
	for i := range table {
		o := &table[i]
		byName[o.Name] = o
		if o.Short != 0 {
			byShort[o.Short] = o
		}
	}
	for i := range v0Table {
		for _, f := range flags(v0Table[i].V0) {
			v0Index[f] = &v0Table[i]
		}
	}
	for _, tool := range []string{"curl", "wget", "aria2c"} {
		for i := range table {
			o := &table[i]
			col := map[string]string{"curl": o.Curl, "wget": o.Wget, "aria2c": o.Aria2}[tool]
			for _, f := range flags(col) {
				if !hasTool(otherIndex[f], tool) {
					otherIndex[f] = append(otherIndex[f], otherFlag{tool: tool, opt: o})
				}
			}
		}
	}
}

func hasTool(list []otherFlag, tool string) bool {
	for _, o := range list {
		if o.tool == tool {
			return true
		}
	}
	return false
}

// flags returns the flags that a table cell names, such as "-o / -O".
func flags(cell string) []string {
	var out []string
	for _, m := range flagToken.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[2])
	}
	return out
}

// lookupLong finds a long option. negated is set for --no-<bool>. Each
// --tls-* option has a --proxy-tls-* form with the same type.
func lookupLong(name string) (o *Option, negated bool) {
	if o := byName[name]; o != nil {
		return o, false
	}
	if rest, ok := strings.CutPrefix(name, "no-"); ok {
		// A --no- form of an option that is not on/off is found too, so the
		// parser can say that it has none.
		if o, _ := lookupLong(rest); o != nil {
			return o, true
		}
	}
	if rest, ok := strings.CutPrefix(name, "proxy-"); ok && strings.HasPrefix(rest, "tls-") {
		if base := byName[rest]; base != nil {
			proxyOpt := *base
			proxyOpt.Name = name
			proxyOpt.Group = "proxy"
			return &proxyOpt, false
		}
	}
	return nil, false
}

func lookupShort(c byte) *Option { return byShort[c] }

// unknown is the error for a flag that is not a v1 option. It teaches the
// v1 form: of a v0 flag, of a curl, wget or aria2 flag, or of a typo.
func unknown(flag string) error {
	name, _, _ := strings.Cut(flag, "=")
	if v0 := v0Index[name]; v0 != nil {
		msg := fmt.Sprintf("%s is a ghola v0 flag. In v1 it is %s.", name, v0.V1)
		if v0.V1 == "(default)" {
			msg = fmt.Sprintf("%s is a ghola v0 flag. In v1 this is the default.", name)
		}
		if v0.Note != "" {
			msg += " " + v0.Note
		}
		return &UsageError{Msg: msg}
	}
	if others := otherIndex[name]; len(others) > 0 {
		return &UsageError{Msg: otherMessage(name, others)}
	}
	if near := nearest(strings.TrimLeft(name, "-")); near != "" {
		return usagef("%s is not a ghola option. Did you mean --%s?", name, near)
	}
	return usagef("%s is not a ghola option. ghola --help lists the options.", name)
}

func otherMessage(flag string, others []otherFlag) string {
	if len(others) == 1 {
		return fmt.Sprintf("%s is a %s flag. In ghola it is %s. To run %s syntax as it is, use ghola %s.",
			flag, others[0].tool, nativeForm(others[0].opt), others[0].tool, others[0].tool)
	}
	parts := make([]string, len(others))
	for i, o := range others {
		parts[i] = fmt.Sprintf("in %s it is ghola %s", o.tool, nativeForm(o.opt))
	}
	return fmt.Sprintf("%s means different things in other tools: %s. To run their syntax as it is, use ghola curl, ghola wget or ghola aria2c.",
		flag, strings.Join(parts, "; "))
}

// nativeForm names an option with its letter, and says when it is on by
// default.
func nativeForm(o *Option) string {
	s := "--" + o.Name
	if o.Short != 0 {
		s += fmt.Sprintf(" (-%c)", o.Short)
	}
	if o.Type == typeBool && o.Default == "true" {
		s += fmt.Sprintf(", and it is on by default: --no-%s turns it off", o.Name)
	}
	return s
}

// nearest returns the option name at edit distance 2 or less, the closest
// first, or "".
func nearest(name string) string {
	type cand struct {
		name string
		d    int
	}
	var best []cand
	for n := range byName {
		if d := distance(name, n); d <= 2 {
			best = append(best, cand{n, d})
		}
	}
	if len(best) == 0 {
		return ""
	}
	sort.Slice(best, func(i, j int) bool {
		if best[i].d != best[j].d {
			return best[i].d < best[j].d
		}
		return best[i].name < best[j].name
	})
	return best[0].name
}

// distance is the Levenshtein edit distance.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
