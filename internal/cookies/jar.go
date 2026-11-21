package cookies

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
	"golang.org/x/net/publicsuffix"
)

// Entry is a persisted cookie record.
type Entry struct {
	Name    string     `json:"name"`
	Value   string     `json:"value"`
	Domain  string     `json:"domain"`
	Path    string     `json:"path"`
	Expires *time.Time `json:"expires,omitempty"`
	Secure  bool       `json:"secure,omitempty"`
	// HTTPOnly and SameSite are kept for the jar file. ghola is not a
	// browser, so they change nothing when it sends a cookie.
	HTTPOnly bool   `json:"http_only,omitempty"`
	SameSite string `json:"same_site,omitempty"`
	// HostOnly is set for a cookie without a Domain attribute: it goes only
	// to the host that set it (RFC 6265, 5.3 step 6). A v0 JSON jar has no
	// such field, so its entries keep the v0 domain matching.
	HostOnly bool `json:"host_only,omitempty"`
}

// The jar file formats. A new jar is Netscape, the format of curl and wget,
// so a jar works with all three tools. A v0 JSON jar is read and written
// back as JSON.
const (
	formatNetscape = "netscape"
	formatJSON     = "json"
)

// Jar is a cookie jar that follows the storage and matching rules of
// RFC 6265, backed by a file in the Netscape or the v0 JSON format.
type Jar struct {
	path    string
	format  string
	mu      sync.Mutex
	entries []Entry
}

// New creates a new jar and loads it if a path is provided.
func New(path string) (*Jar, error) {
	j := &Jar{path: path, format: formatNetscape}
	if path != "" {
		if err := j.Load(); err != nil {
			return nil, err
		}
	}
	return j, nil
}

// Load reads the jar from disk if it exists. The format is detected: a
// file that starts with [ is a v0 JSON jar, anything else is Netscape.
func (j *Jar) Load() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path == "" {
		return nil
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "[") {
		j.format = formatJSON
		return json.Unmarshal(data, &j.entries)
	}
	j.entries, err = parseNetscape(text)
	return err
}

// Save writes the jar back to disk, in the format it was read in.
func (j *Jar) Save() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0755); err != nil {
		return err
	}
	var data []byte
	if j.format == formatJSON {
		var err error
		if data, err = json.MarshalIndent(j.entries, "", "  "); err != nil {
			return err
		}
	} else {
		data = []byte(formatNetscapeFile(j.entries))
	}
	return os.WriteFile(j.path, data, 0644)
}

// AddRequestCookies seeds the jar from CLI cookie flags. A seeded cookie is
// host-only: it goes to the host of the URL.
func (j *Jar) AddRequestCookies(rawURL string, values []string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 {
			continue
		}
		j.upsert(Entry{
			Name:     strings.TrimSpace(parts[0]),
			Value:    strings.TrimSpace(parts[1]),
			Domain:   strings.ToLower(u.Hostname()),
			Path:     "/",
			HostOnly: true,
		})
	}
	return nil
}

// HeaderValue returns the Cookie header value for the given URL.
func (j *Jar) HeaderValue(rawURL string) string {
	j.mu.Lock()
	defer j.mu.Unlock()

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	now := time.Now()
	pairs := make([]string, 0, len(j.entries))
	filtered := make([]Entry, 0, len(j.entries))
	for _, entry := range j.entries {
		if entry.Expires != nil && entry.Expires.Before(now) {
			continue
		}
		filtered = append(filtered, entry)
		if matches(u, entry) {
			pairs = append(pairs, entry.Name+"="+entry.Value)
		}
	}
	j.entries = filtered
	return strings.Join(pairs, "; ")
}

// AbsorbResponse stores response cookies for future requests.
func (j *Jar) AbsorbResponse(rawURL string, rsp *fasthttp.Response) {
	var values []string
	for key, value := range rsp.Header.All() {
		if strings.EqualFold(string(key), "Set-Cookie") {
			values = append(values, string(value))
		}
	}
	j.AbsorbSetCookies(rawURL, values)
}

// AbsorbSetCookies stores the cookies of Set-Cookie values that the
// response to rawURL sent, by the rules of RFC 6265, section 5.3. A cookie
// for a domain that the host does not belong to, or for a public suffix,
// is dropped. Max-Age=0 or an expiry in the past deletes the cookie.
func (j *Jar) AbsorbSetCookies(rawURL string, values []string) {
	u, err := url.Parse(rawURL)
	if err != nil || len(values) == 0 {
		return
	}
	headers := http.Header{"Set-Cookie": values}
	now := time.Now()
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		entry, ok := storable(u, cookie, now)
		if !ok {
			continue
		}
		if entry.Expires != nil && !entry.Expires.After(now) {
			j.remove(entry)
			continue
		}
		j.upsert(entry)
	}
}

// storable returns the jar entry for a cookie that the response to u set,
// or false when RFC 6265 refuses it.
func storable(u *url.URL, cookie *http.Cookie, now time.Time) (Entry, bool) {
	host := strings.ToLower(u.Hostname())
	entry := Entry{
		Name: cookie.Name, Value: cookie.Value, Domain: host, HostOnly: true,
		Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly,
		SameSite: sameSiteString(cookie.SameSite),
	}
	if d := strings.ToLower(strings.TrimPrefix(cookie.Domain, ".")); d != "" && d != host {
		if suffix, _ := publicsuffix.PublicSuffix(d); suffix == d {
			return Entry{}, false // a public suffix (5.3 step 5)
		}
		if !domainMatch(host, d) {
			return Entry{}, false // not the host's domain (5.3 step 6)
		}
		entry.Domain, entry.HostOnly = d, false
	}
	if entry.Path == "" || !strings.HasPrefix(entry.Path, "/") {
		entry.Path = defaultPath(u)
	}
	switch {
	case cookie.MaxAge < 0: // Max-Age=0 or below: delete
		past := now.Add(-time.Second)
		entry.Expires = &past
	case cookie.MaxAge > 0: // Max-Age wins over Expires (5.3 step 3)
		exp := now.Add(time.Duration(cookie.MaxAge) * time.Second)
		entry.Expires = &exp
	case !cookie.Expires.IsZero():
		exp := cookie.Expires
		entry.Expires = &exp
	}
	return entry, true
}

// defaultPath is the default-path of RFC 6265, 5.1.4: the request path up
// to its last slash.
func defaultPath(u *url.URL) string {
	p := u.EscapedPath()
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func (j *Jar) upsert(entry Entry) {
	j.mu.Lock()
	defer j.mu.Unlock()

	for i, existing := range j.entries {
		if sameCookie(existing, entry) {
			j.entries[i] = entry
			return
		}
	}
	j.entries = append(j.entries, entry)
}

func (j *Jar) remove(entry Entry) {
	j.mu.Lock()
	defer j.mu.Unlock()

	kept := j.entries[:0]
	for _, existing := range j.entries {
		if !sameCookie(existing, entry) {
			kept = append(kept, existing)
		}
	}
	j.entries = kept
}

func sameCookie(a, b Entry) bool {
	return a.Name == b.Name &&
		strings.EqualFold(a.Domain, b.Domain) &&
		a.Path == b.Path
}

// domainMatch is the domain-match of RFC 6265, 5.1.3. An IP address matches
// only itself.
func domainMatch(host, domain string) bool {
	if host == domain {
		return true
	}
	return net.ParseIP(host) == nil && strings.HasSuffix(host, "."+domain)
}

// pathMatch is the path-match of RFC 6265, 5.1.4: /foo matches /foo and
// /foo/bar, not /foobar.
func pathMatch(reqPath, cookiePath string) bool {
	if cookiePath == "" || reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

func matches(u *url.URL, entry Entry) bool {
	host := strings.ToLower(u.Hostname())
	domain := strings.ToLower(strings.TrimPrefix(entry.Domain, "."))
	switch {
	case entry.HostOnly && host != domain:
		return false
	case !entry.HostOnly && domain != "" && !domainMatch(host, domain):
		return false
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !pathMatch(path, entry.Path) {
		return false
	}
	return !entry.Secure || u.Scheme == "https"
}

func sameSiteString(s http.SameSite) string {
	switch s {
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return ""
	}
}
