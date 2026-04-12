package profile

import (
	"strings"
	"testing"
)

func TestResolve_KnownProfiles(t *testing.T) {
	for _, name := range []string{"chrome", "firefox", "safari", "edge"} {
		t.Run(name, func(t *testing.T) {
			p, err := Resolve(name)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", name, err)
			}
			if p.Name != name {
				t.Errorf("Name = %q, want %q", p.Name, name)
			}
			if p.UserAgent == "" {
				t.Error("UserAgent is empty")
			}
			if p.TLSClientProfile == "" {
				t.Error("TLSClientProfile is empty")
			}
		})
	}
}

func TestResolve_Unknown(t *testing.T) {
	_, err := Resolve("netscape")
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("error = %q, want 'unknown profile'", err)
	}
}

func TestResolve_Empty(t *testing.T) {
	p, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve empty: %v", err)
	}
	def := DefaultProfile()
	if p.Family != def.Family {
		t.Errorf("Family = %q, want default %q", p.Family, def.Family)
	}
}

func TestResolve_VersionedName(t *testing.T) {
	p, err := Resolve("chrome-120")
	if err != nil {
		t.Fatalf("Resolve(chrome-120): %v", err)
	}
	if p.Family != "chrome" {
		t.Errorf("Family = %q, want chrome", p.Family)
	}
	if p.Name != "chrome-120" {
		t.Errorf("Name = %q, want chrome-120", p.Name)
	}
}

func TestResolve_CaseInsensitive(t *testing.T) {
	p, err := Resolve("  Chrome  ")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Family != "chrome" {
		t.Errorf("Family = %q, want chrome", p.Family)
	}
}

func TestBuildHeaders_StealthDisabled(t *testing.T) {
	p := DefaultProfile()
	headers := BuildHeaders(p, HeaderOptions{StealthHeaders: false})
	if headers != nil {
		t.Errorf("expected nil headers when stealth disabled, got %d", len(headers))
	}
}

func TestBuildHeaders_ChromeFamily(t *testing.T) {
	p, _ := Resolve("chrome")
	headers := BuildHeaders(p, HeaderOptions{
		Method:         "GET",
		TargetURL:      "https://example.com",
		StealthHeaders: true,
	})
	if len(headers) == 0 {
		t.Fatal("expected headers for chrome stealth")
	}

	found := make(map[string]bool)
	for _, h := range headers {
		found[h.Key] = true
	}

	for _, expected := range []string{"User-Agent", "Accept", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "Sec-Fetch-Site"} {
		if !found[expected] {
			t.Errorf("missing expected header %q", expected)
		}
	}
}

func TestBuildHeaders_FirefoxFamily(t *testing.T) {
	p, _ := Resolve("firefox")
	headers := BuildHeaders(p, HeaderOptions{
		Method:         "GET",
		TargetURL:      "https://example.com",
		StealthHeaders: true,
	})

	for _, h := range headers {
		if strings.HasPrefix(h.Key, "sec-ch-ua") {
			t.Errorf("Firefox should not have sec-ch-ua headers, found %q", h.Key)
		}
	}
}

func TestBuildHeaders_SafariFamily(t *testing.T) {
	p, _ := Resolve("safari")
	headers := BuildHeaders(p, HeaderOptions{
		Method:         "GET",
		TargetURL:      "https://example.com",
		StealthHeaders: true,
	})

	found := make(map[string]bool)
	for _, h := range headers {
		found[h.Key] = true
	}

	if !found["Upgrade-Insecure-Requests"] {
		t.Error("Safari should have Upgrade-Insecure-Requests")
	}
	if found["Sec-Fetch-Site"] {
		t.Error("Safari should not have Sec-Fetch-Site")
	}
}

func TestBuildHeaders_RefererModes(t *testing.T) {
	p := DefaultProfile()

	tests := []struct {
		mode string
		want string
	}{
		{"none", ""},
		{"auto", "https://www.google.com/"},
		{"https://example.com/ref", "https://example.com/ref"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			headers := BuildHeaders(p, HeaderOptions{
				StealthHeaders: true,
				RefererMode:    tt.mode,
			})
			var got string
			for _, h := range headers {
				if h.Key == "Referer" {
					got = h.Value
				}
			}
			if got != tt.want {
				t.Errorf("Referer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildHeaders_ExplicitUA(t *testing.T) {
	p := DefaultProfile()
	headers := BuildHeaders(p, HeaderOptions{
		StealthHeaders: true,
		ExplicitUA:     "MyBot/1.0",
	})
	for _, h := range headers {
		if h.Key == "User-Agent" {
			if h.Value != "MyBot/1.0" {
				t.Errorf("UA = %q, want MyBot/1.0", h.Value)
			}
			return
		}
	}
	t.Error("User-Agent header not found")
}

func TestBuildHeaders_ExplicitLang(t *testing.T) {
	p := DefaultProfile()
	headers := BuildHeaders(p, HeaderOptions{
		StealthHeaders: true,
		ExplicitLang:   "fr-FR",
	})
	for _, h := range headers {
		if h.Key == "Accept-Language" {
			if h.Value != "fr-FR" {
				t.Errorf("Accept-Language = %q, want fr-FR", h.Value)
			}
			return
		}
	}
	t.Error("Accept-Language header not found")
}

func TestProfileNames(t *testing.T) {
	names := ProfileNames()
	if len(names) != 6 {
		t.Errorf("ProfileNames count = %d, want 6", len(names))
	}
	expected := map[string]bool{"chrome": true, "firefox": true, "safari": true, "edge": true, "chrome-mobile": true, "safari-mobile": true}
	for _, n := range names {
		if !expected[n] {
			t.Errorf("unexpected profile name %q", n)
		}
	}
}

func TestListProfiles(t *testing.T) {
	all := ListProfiles()
	if len(all) != 6 {
		t.Errorf("ListProfiles count = %d, want 6", len(all))
	}
}

func TestResolve_MobileProfiles(t *testing.T) {
	for _, name := range []string{"chrome-mobile", "safari-mobile"} {
		t.Run(name, func(t *testing.T) {
			p, err := Resolve(name)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", name, err)
			}
			if p.Name != name {
				t.Errorf("Name = %q, want %q", p.Name, name)
			}
		})
	}
}

func TestTLSClientProfileName(t *testing.T) {
	p, _ := Resolve("chrome")
	if got := p.TLSClientProfileName(); got == "" {
		t.Error("TLSClientProfileName is empty")
	}
}

func TestBuildHeaders_SecFetchUserOnlyGET(t *testing.T) {
	p, _ := Resolve("chrome")

	getHeaders := BuildHeaders(p, HeaderOptions{Method: "GET", StealthHeaders: true})
	postHeaders := BuildHeaders(p, HeaderOptions{Method: "POST", StealthHeaders: true})

	hasSecFetchUser := func(headers []HeaderKV) bool {
		for _, h := range headers {
			if h.Key == "Sec-Fetch-User" {
				return true
			}
		}
		return false
	}

	if !hasSecFetchUser(getHeaders) {
		t.Error("GET should have Sec-Fetch-User")
	}
	if hasSecFetchUser(postHeaders) {
		t.Error("POST should not have Sec-Fetch-User")
	}
}
