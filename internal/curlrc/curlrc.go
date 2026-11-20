// Package curlrc reads curl's -K config files into command-line arguments,
// for "ghola curl -K" (v1 spec, section 6). The rules come from the curl
// 8.18.0 manual and were checked against curl 8.18.0 on 2026-11-20:
//
//   - One option for each line. A long option may drop its dashes; it is
//     then separated from its value by whitespace, ":" or "=". An option
//     with dashes is separated by whitespace only.
//   - A value with whitespace, or one that starts with ":" or "=", is in
//     double quotes, with the escapes \\ \" \t \n \r \v. A backslash before
//     any other letter is dropped.
//   - An unquoted value ends at the first whitespace; the rest of the line
//     is ignored. A quote inside an unquoted value is a plain character.
//   - A line whose first non-blank character is # is a comment.
package curlrc

import (
	"fmt"
	"strings"
)

// Args returns the arguments of a config text, as curl would read them
// from a command line: "url = x" gives "--url", "x".
func Args(text string) ([]string, error) {
	var args []string
	for n, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimLeft(line, " \t")
		if line == "" || line[0] == '#' {
			continue
		}
		opt, value, hasValue, err := splitLine(line)
		if err != nil {
			return nil, fmt.Errorf("config line %d: %w", n+1, err)
		}
		args = append(args, opt)
		if hasValue {
			args = append(args, value)
		}
	}
	return args, nil
}

// splitLine returns the option (with its dashes) and its value.
func splitLine(line string) (opt, value string, hasValue bool, err error) {
	end := strings.IndexAny(line, " \t")
	dashed := line[0] == '-'
	if !dashed {
		if i := strings.IndexAny(line, " \t:="); i >= 0 {
			end = i
		}
	}
	if end < 0 {
		end = len(line)
	}
	opt = line[:end]
	if !dashed {
		opt = "--" + opt
	}
	rest := strings.TrimLeft(line[end:], " \t")
	if !dashed && rest != "" && (rest[0] == ':' || rest[0] == '=') {
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	if rest == "" {
		return opt, "", false, nil
	}
	if rest[0] == '"' {
		value, err = unquote(rest)
		return opt, value, err == nil, err
	}
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		rest = rest[:i]
	}
	return opt, rest, true, nil
}

// unquote reads the quoted value at the start of s.
func unquote(s string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			return b.String(), nil
		case c == '\\' && i+1 < len(s):
			i++
			if r, ok := escapes[s[i]]; ok {
				b.WriteByte(r)
			} else {
				b.WriteByte(s[i]) // curl drops the backslash before any other letter
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", fmt.Errorf("a quote is not closed")
}

var escapes = map[byte]byte{'\\': '\\', '"': '"', 't': '\t', 'n': '\n', 'r': '\r', 'v': '\v'}
