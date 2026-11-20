package cookies

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The Netscape cookie file format, as curl and wget read and write it. One
// cookie for each line, seven fields separated by tabs:
//
//	domain  include-subdomains  path  secure  expires  name  value
//
// A domain cookie has a leading dot and TRUE in the second field. A host-only
// cookie has FALSE. expires is a Unix time, 0 for a session cookie. curl
// marks an HttpOnly cookie with the prefix "#HttpOnly_" on the domain. Other
// lines that start with # are comments.

const httpOnlyPrefix = "#HttpOnly_"

const netscapeHeader = "# Netscape HTTP Cookie File\n# This file was written by ghola. It is the format of curl and wget.\n\n"

func parseNetscape(text string) ([]Entry, error) {
	var entries []Entry
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		httpOnly := strings.HasPrefix(line, httpOnlyPrefix)
		if httpOnly {
			line = line[len(httpOnlyPrefix):]
		}
		if line == "" || (!httpOnly && strings.HasPrefix(line, "#")) {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return nil, fmt.Errorf("cookie jar line %d: %d fields, want 7", n+1, len(f))
		}
		e := Entry{
			Domain:   strings.ToLower(strings.TrimPrefix(f[0], ".")),
			HostOnly: !strings.EqualFold(f[1], "TRUE"),
			Path:     f[2],
			Secure:   strings.EqualFold(f[3], "TRUE"),
			Name:     f[5],
			Value:    f[6],
			HTTPOnly: httpOnly,
		}
		exp, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cookie jar line %d: bad expiry %q", n+1, f[4])
		}
		if exp > 0 {
			t := time.Unix(exp, 0)
			e.Expires = &t
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func formatNetscapeFile(entries []Entry) string {
	var b strings.Builder
	b.WriteString(netscapeHeader)
	for _, e := range entries {
		domain, sub := e.Domain, "FALSE"
		if !e.HostOnly {
			domain, sub = "."+strings.TrimPrefix(e.Domain, "."), "TRUE"
		}
		if e.HTTPOnly {
			domain = httpOnlyPrefix + domain
		}
		var exp int64
		if e.Expires != nil {
			exp = e.Expires.Unix()
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", domain, sub, e.Path, boolField(e.Secure), exp, e.Name, e.Value)
	}
	return b.String()
}

func boolField(v bool) string {
	if v {
		return "TRUE"
	}
	return "FALSE"
}
