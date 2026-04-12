package cookies

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

func TestNew_EmptyPath(t *testing.T) {
	jar, err := New("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jar.path != "" {
		t.Errorf("path = %q, want empty", jar.path)
	}
}

func TestNew_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")

	entries := []Entry{{Name: "tok", Value: "abc", Domain: "example.com", Path: "/"}}
	data, _ := json.Marshal(entries)
	os.WriteFile(path, data, 0644)

	jar, err := New(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hdr := jar.HeaderValue("https://example.com/")
	if hdr != "tok=abc" {
		t.Errorf("HeaderValue = %q, want %q", hdr, "tok=abc")
	}
}

func TestNew_MissingFile(t *testing.T) {
	jar, err := New(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if jar == nil {
		t.Fatal("jar is nil")
	}
}

func TestSave_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path := filepath.Join(dir, "cookies.json")

	jar, _ := New("")
	jar.path = path
	jar.entries = []Entry{{Name: "k", Value: "v", Domain: "test.com", Path: "/"}}

	if err := jar.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSave_NoOpWithoutPath(t *testing.T) {
	jar, _ := New("")
	if err := jar.Save(); err != nil {
		t.Fatalf("Save with empty path should be no-op: %v", err)
	}
}

func TestAddRequestCookies(t *testing.T) {
	jar, _ := New("")
	err := jar.AddRequestCookies("https://example.com/api", []string{"session=xyz", "lang=en"})
	if err != nil {
		t.Fatalf("AddRequestCookies: %v", err)
	}

	hdr := jar.HeaderValue("https://example.com/api")
	if hdr != "session=xyz; lang=en" {
		t.Errorf("HeaderValue = %q, want %q", hdr, "session=xyz; lang=en")
	}
}

func TestAddRequestCookies_MalformedSkipped(t *testing.T) {
	jar, _ := New("")
	err := jar.AddRequestCookies("https://example.com/", []string{"noequals", "good=val"})
	if err != nil {
		t.Fatalf("AddRequestCookies: %v", err)
	}
	hdr := jar.HeaderValue("https://example.com/")
	if hdr != "good=val" {
		t.Errorf("HeaderValue = %q, want %q", hdr, "good=val")
	}
}

func TestHeaderValue_DomainMatching(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		reqURL  string
		wantHit bool
	}{
		{"exact match", "example.com", "https://example.com/", true},
		{"subdomain match", "example.com", "https://sub.example.com/", true},
		{"dot prefix domain", ".example.com", "https://sub.example.com/", true},
		{"no match", "other.com", "https://example.com/", false},
		{"partial name mismatch", "ample.com", "https://example.com/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jar, _ := New("")
			jar.entries = []Entry{{Name: "c", Value: "1", Domain: tt.domain, Path: "/"}}
			hdr := jar.HeaderValue(tt.reqURL)
			gotHit := hdr != ""
			if gotHit != tt.wantHit {
				t.Errorf("HeaderValue(%q) hit=%v, want hit=%v (hdr=%q)", tt.reqURL, gotHit, tt.wantHit, hdr)
			}
		})
	}
}

func TestHeaderValue_PathMatching(t *testing.T) {
	jar, _ := New("")
	jar.entries = []Entry{{Name: "c", Value: "1", Domain: "example.com", Path: "/api"}}

	if hdr := jar.HeaderValue("https://example.com/api/users"); hdr == "" {
		t.Error("expected match for /api/users with path /api")
	}
	if hdr := jar.HeaderValue("https://example.com/other"); hdr != "" {
		t.Errorf("expected no match for /other, got %q", hdr)
	}
}

func TestHeaderValue_ExpiryPurge(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour)
	jar, _ := New("")
	jar.entries = []Entry{
		{Name: "expired", Value: "old", Domain: "example.com", Path: "/", Expires: &past},
		{Name: "fresh", Value: "new", Domain: "example.com", Path: "/"},
	}

	hdr := jar.HeaderValue("https://example.com/")
	if hdr != "fresh=new" {
		t.Errorf("HeaderValue = %q, want %q (expired cookie should be purged)", hdr, "fresh=new")
	}

	// Verify the expired entry was actually removed from the slice.
	jar.mu.Lock()
	count := len(jar.entries)
	jar.mu.Unlock()
	if count != 1 {
		t.Errorf("entries count = %d, want 1 after expiry purge", count)
	}
}

func TestHeaderValue_SecureCookies(t *testing.T) {
	jar, _ := New("")
	jar.entries = []Entry{{Name: "sec", Value: "v", Domain: "example.com", Path: "/", Secure: true}}

	if hdr := jar.HeaderValue("https://example.com/"); hdr == "" {
		t.Error("secure cookie should match https")
	}
	if hdr := jar.HeaderValue("http://example.com/"); hdr != "" {
		t.Errorf("secure cookie should not match http, got %q", hdr)
	}
}

func TestAbsorbResponse(t *testing.T) {
	jar, _ := New("")
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.Set("Set-Cookie", "token=abc123; Path=/; Domain=example.com")

	jar.AbsorbResponse("https://example.com/login", rsp)

	hdr := jar.HeaderValue("https://example.com/")
	if hdr != "token=abc123" {
		t.Errorf("HeaderValue after absorb = %q, want %q", hdr, "token=abc123")
	}
}

func TestAbsorbResponse_InfersDomain(t *testing.T) {
	jar, _ := New("")
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)

	rsp.Header.Set("Set-Cookie", "k=v; Path=/")

	jar.AbsorbResponse("https://mysite.com/page", rsp)

	hdr := jar.HeaderValue("https://mysite.com/")
	if hdr != "k=v" {
		t.Errorf("HeaderValue = %q, want %q", hdr, "k=v")
	}
}

func TestUpsert_UpdateExisting(t *testing.T) {
	jar, _ := New("")
	jar.upsert(Entry{Name: "tok", Value: "v1", Domain: "example.com", Path: "/"})
	jar.upsert(Entry{Name: "tok", Value: "v2", Domain: "example.com", Path: "/"})

	hdr := jar.HeaderValue("https://example.com/")
	if hdr != "tok=v2" {
		t.Errorf("HeaderValue = %q, want %q after upsert", hdr, "tok=v2")
	}

	jar.mu.Lock()
	count := len(jar.entries)
	jar.mu.Unlock()
	if count != 1 {
		t.Errorf("entries count = %d, want 1 after upsert of same cookie", count)
	}
}

func TestRoundTrip_SaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jar.json")

	jar1, _ := New(path)
	jar1.upsert(Entry{Name: "sess", Value: "abc", Domain: "example.com", Path: "/"})
	if err := jar1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	jar2, err := New(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	hdr := jar2.HeaderValue("https://example.com/")
	if hdr != "sess=abc" {
		t.Errorf("after reload HeaderValue = %q, want %q", hdr, "sess=abc")
	}
}
