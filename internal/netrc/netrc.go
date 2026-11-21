// Package netrc reads .netrc files for --auth-netrc and --auth-netrc-file
// (plan phase 2, item 4). The rules come from curl 8.18.0, checked on
// 2026-11-20 against a server that echoes the credentials it gets:
//
//   - The first "machine" entry for the host wins; "default" serves a host
//     with no entry.
//   - With a user in the URL, the entry with that login wins.
//   - Keywords are case-insensitive. A "macdef" body runs to the next blank
//     line and is skipped. A value may be quoted, with \" \\ \n \r \t.
//   - From # to the end of the line is a comment.
//   - A login without a password gives an empty password.
//
// Credentials from -u win over the file; the caller applies that rule.
package netrc

import (
	"fmt"
	"os"
	"strings"
)

// Entry is one machine (or the default) of a netrc file.
type Entry struct {
	Machine  string // "" for the default entry
	Login    string
	Password string
	Default  bool
}

// ParseFile reads a netrc file.
func ParseFile(path string) ([]Entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b))
}

// Parse reads the entries of a netrc text.
func Parse(text string) ([]Entry, error) {
	toks, err := tokens(text)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	var cur *Entry
	for i := 0; i < len(toks); i++ {
		switch strings.ToLower(toks[i]) {
		case "machine":
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("netrc: machine without a name")
			}
			i++
			entries = append(entries, Entry{Machine: toks[i]})
			cur = &entries[len(entries)-1]
		case "default":
			entries = append(entries, Entry{Default: true})
			cur = &entries[len(entries)-1]
		case "login", "password", "account":
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("netrc: %s without a value", toks[i])
			}
			key := strings.ToLower(toks[i])
			i++
			if cur == nil {
				continue // a value before any machine belongs to no entry
			}
			switch key {
			case "login":
				cur.Login = toks[i]
			case "password":
				cur.Password = toks[i]
			}
		}
	}
	return entries, nil
}

// Find returns the entry for host: the first machine entry for host (with
// login user, when user is set), else the default entry.
func Find(entries []Entry, host, user string) (Entry, bool) {
	var dflt *Entry
	for i := range entries {
		e := &entries[i]
		if e.Default {
			if dflt == nil {
				dflt = e
			}
			continue
		}
		if strings.EqualFold(e.Machine, host) && (user == "" || e.Login == user) {
			return *e, true
		}
	}
	if dflt != nil && (user == "" || dflt.Login == user) {
		return *dflt, true
	}
	return Entry{}, false
}

// tokens splits a netrc text into words. It drops comments and macdef
// bodies, and reads quoted words.
func tokens(text string) ([]string, error) {
	var out []string
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for n := 0; n < len(lines); n++ {
		words, err := lineTokens(lines[n])
		if err != nil {
			return nil, fmt.Errorf("netrc line %d: %w", n+1, err)
		}
		for _, w := range words {
			out = append(out, w)
			if strings.EqualFold(w, "macdef") {
				for n+1 < len(lines) && strings.TrimSpace(lines[n+1]) != "" {
					n++ // the macro body
				}
				break
			}
		}
	}
	return out, nil
}

func lineTokens(line string) ([]string, error) {
	var out []string
	for i := 0; i < len(line); {
		switch line[i] {
		case ' ', '\t':
			i++
		case '#':
			return out, nil
		case '"':
			w, end, err := quoted(line, i)
			if err != nil {
				return nil, err
			}
			out = append(out, w)
			i = end
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			out = append(out, line[i:j])
			i = j
		}
	}
	return out, nil
}

// quoted reads the quoted word that starts at line[start].
func quoted(line string, start int) (string, int, error) {
	var b strings.Builder
	for i := start + 1; i < len(line); i++ {
		c := line[i]
		if c == '"' {
			return b.String(), i + 1, nil
		}
		if c == '\\' && i+1 < len(line) {
			i++
			switch line[i] {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			default:
				c = line[i]
			}
		}
		b.WriteByte(c)
	}
	return "", 0, fmt.Errorf("a quote is not closed")
}
