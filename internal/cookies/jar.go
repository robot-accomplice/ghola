package cookies

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// Entry is a persisted cookie record.
type Entry struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Domain   string     `json:"domain"`
	Path     string     `json:"path"`
	Expires  *time.Time `json:"expires,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
	HTTPOnly bool       `json:"http_only,omitempty"`
}

// Jar is a JSON-backed cookie jar.
type Jar struct {
	path    string
	mu      sync.Mutex
	entries []Entry
}

// New creates a new jar and loads it if a path is provided.
func New(path string) (*Jar, error) {
	j := &Jar{path: path}
	if path != "" {
		if err := j.Load(); err != nil {
			return nil, err
		}
	}
	return j, nil
}

// Load reads the jar from disk if it exists.
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
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &j.entries)
}

// Save writes the jar back to disk when persistence is enabled.
func (j *Jar) Save() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(j.path, data, 0644)
}

// AddRequestCookies seeds the jar from CLI cookie flags.
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
			Name:   strings.TrimSpace(parts[0]),
			Value:  strings.TrimSpace(parts[1]),
			Domain: u.Hostname(),
			Path:   "/",
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
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}

	headers := make(http.Header)
	rsp.Header.VisitAll(func(key, value []byte) {
		if strings.EqualFold(string(key), "Set-Cookie") {
			headers.Add("Set-Cookie", string(value))
		}
	})
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		domain := cookie.Domain
		if domain == "" {
			domain = u.Hostname()
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		var expires *time.Time
		if !cookie.Expires.IsZero() {
			expires = &cookie.Expires
		}
		j.upsert(Entry{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   domain,
			Path:     path,
			Expires:  expires,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HttpOnly,
		})
	}
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

func sameCookie(a, b Entry) bool {
	return strings.EqualFold(a.Name, b.Name) &&
		strings.EqualFold(a.Domain, b.Domain) &&
		a.Path == b.Path
}

func matches(u *url.URL, entry Entry) bool {
	host := strings.ToLower(u.Hostname())
	domain := strings.ToLower(strings.TrimPrefix(entry.Domain, "."))
	if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if entry.Path != "" && !strings.HasPrefix(path, entry.Path) {
		return false
	}
	if entry.Secure && u.Scheme != "https" {
		return false
	}
	return true
}
