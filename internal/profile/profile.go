package profile

import (
	"fmt"
	"net/url"
	"strings"
)

// HeaderKV preserves generated header ordering.
type HeaderKV struct {
	Key   string
	Value string
}

// BrowserProfile describes a browser-like request profile.
type BrowserProfile struct {
	Name             string
	Family           string
	Version          string
	TLSClientProfile string
	UserAgent        string
	Accept           string
	AcceptLanguage   string
	AcceptEncoding   string
	SecCHUA          string
	SecCHUAMobile    string
	SecCHUAPlatform  string
	SupportsHTTP3    bool
}

// HeaderOptions controls how generated headers are built.
type HeaderOptions struct {
	Method         string
	TargetURL      string
	RefererMode    string
	ExplicitUA     string
	ExplicitLang   string
	StealthHeaders bool
}

var profiles = []BrowserProfile{
	{
		Name:             "chrome",
		Family:           "chrome",
		Version:          "146",
		TLSClientProfile: "chrome_146",
		UserAgent:        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.9",
		AcceptEncoding:   "gzip, deflate, br",
		SecCHUA:          `"Google Chrome";v="146", "Chromium";v="146", "Not.A/Brand";v="24"`,
		SecCHUAMobile:    "?0",
		SecCHUAPlatform:  `"macOS"`,
		SupportsHTTP3:    true,
	},
	{
		Name:             "firefox",
		Family:           "firefox",
		Version:          "147",
		TLSClientProfile: "firefox_147",
		UserAgent:        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:147.0) Gecko/20100101 Firefox/147.0",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.5",
		AcceptEncoding:   "gzip, deflate, br",
		SupportsHTTP3:    true,
	},
	{
		Name:             "safari",
		Family:           "safari",
		Version:          "18.0",
		TLSClientProfile: "safari_16_0", // nearest available tls-client mapping
		UserAgent:        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.9",
		AcceptEncoding:   "gzip, deflate, br",
		SupportsHTTP3:    false,
	},
	{
		Name:             "edge",
		Family:           "edge",
		Version:          "146",
		TLSClientProfile: "chrome_146",
		UserAgent:        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36 Edg/146.0.0.0",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.9",
		AcceptEncoding:   "gzip, deflate, br",
		SecCHUA:          `"Microsoft Edge";v="146", "Chromium";v="146", "Not.A/Brand";v="24"`,
		SecCHUAMobile:    "?0",
		SecCHUAPlatform:  `"macOS"`,
		SupportsHTTP3:    true,
	},
	{
		Name:             "chrome-mobile",
		Family:           "chrome",
		Version:          "146",
		TLSClientProfile: "chrome_146",
		UserAgent:        "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Mobile Safari/537.36",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.9",
		AcceptEncoding:   "gzip, deflate, br",
		SecCHUA:          `"Google Chrome";v="146", "Chromium";v="146", "Not.A/Brand";v="24"`,
		SecCHUAMobile:    "?1",
		SecCHUAPlatform:  `"Android"`,
		SupportsHTTP3:    true,
	},
	{
		Name:             "safari-mobile",
		Family:           "safari",
		Version:          "18.0",
		TLSClientProfile: "safari_16_0",
		UserAgent:        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
		Accept:           "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:   "en-US,en;q=0.9",
		AcceptEncoding:   "gzip, deflate, br",
		SupportsHTTP3:    false,
	},
}

// DefaultProfile returns the default browser-like profile.
func DefaultProfile() BrowserProfile {
	return profiles[0]
}

// ListProfiles returns the supported browser-like profiles.
func ListProfiles() []BrowserProfile {
	out := make([]BrowserProfile, len(profiles))
	copy(out, profiles)
	return out
}

// Resolve finds the requested profile. Versioned names currently map to the
// nearest built-in family profile while preserving the requested name.
func Resolve(name string) (BrowserProfile, error) {
	if strings.TrimSpace(name) == "" {
		return DefaultProfile(), nil
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, p := range profiles {
		if p.Name == lower || strings.HasPrefix(lower, p.Name+"-") {
			resolved := p
			resolved.Name = lower
			return resolved, nil
		}
	}
	return BrowserProfile{}, fmt.Errorf("unknown profile %q", name)
}

// TLSClientProfileName returns the mapped tls-client profile identifier.
func (p BrowserProfile) TLSClientProfileName() string {
	return p.TLSClientProfile
}

// ProfileNames returns the supported profile names.
func ProfileNames() []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}

// BuildHeaders generates coherent browser-style headers for the selected profile.
func BuildHeaders(p BrowserProfile, opts HeaderOptions) []HeaderKV {
	if !opts.StealthHeaders {
		return nil
	}

	headers := []HeaderKV{
		{Key: "User-Agent", Value: firstNonEmpty(opts.ExplicitUA, p.UserAgent)},
		{Key: "Accept", Value: p.Accept},
		{Key: "Accept-Language", Value: firstNonEmpty(opts.ExplicitLang, p.AcceptLanguage)},
		{Key: "Accept-Encoding", Value: p.AcceptEncoding},
		{Key: "Connection", Value: "keep-alive"},
	}

	if ref := buildReferer(opts.RefererMode, opts.TargetURL); ref != "" {
		headers = append(headers, HeaderKV{Key: "Referer", Value: ref})
	}

	method := strings.ToUpper(opts.Method)
	if method == "" {
		method = "GET"
	}

	switch p.Family {
	case "chrome", "edge":
		headers = append(headers,
			HeaderKV{Key: "Upgrade-Insecure-Requests", Value: "1"},
			HeaderKV{Key: "Sec-Fetch-Site", Value: "none"},
			HeaderKV{Key: "Sec-Fetch-Mode", Value: "navigate"},
			HeaderKV{Key: "Sec-Fetch-Dest", Value: "document"},
			HeaderKV{Key: "sec-ch-ua", Value: p.SecCHUA},
			HeaderKV{Key: "sec-ch-ua-mobile", Value: p.SecCHUAMobile},
			HeaderKV{Key: "sec-ch-ua-platform", Value: p.SecCHUAPlatform},
		)
		if method == "GET" {
			headers = append(headers, HeaderKV{Key: "Sec-Fetch-User", Value: "?1"})
		}
	case "firefox":
		headers = append(headers,
			HeaderKV{Key: "Upgrade-Insecure-Requests", Value: "1"},
			HeaderKV{Key: "Sec-Fetch-Site", Value: "none"},
			HeaderKV{Key: "Sec-Fetch-Mode", Value: "navigate"},
			HeaderKV{Key: "Sec-Fetch-Dest", Value: "document"},
		)
		if method == "GET" {
			headers = append(headers, HeaderKV{Key: "Sec-Fetch-User", Value: "?1"})
		}
	case "safari":
		headers = append(headers, HeaderKV{Key: "Upgrade-Insecure-Requests", Value: "1"})
	}

	return headers
}

func buildReferer(mode, rawURL string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none":
		return ""
	case "auto":
		return "https://www.google.com/"
	default:
		if _, err := url.ParseRequestURI(mode); err == nil {
			return mode
		}
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
