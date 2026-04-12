package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSelector_SingleProxy(t *testing.T) {
	sel, err := NewSelector("http://proxy.local:8080", "", "", "sticky")
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	got, err := sel.Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "http://proxy.local:8080" {
		t.Errorf("Select = %q, want http://proxy.local:8080", got)
	}
}

func TestNewSelector_ProxyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	content := "# comment\nhttp://p1.local:8080\n\nhttp://p2.local:8080\n# another comment\n"
	os.WriteFile(path, []byte(content), 0644)

	sel, err := NewSelector("", "", path, "round-robin")
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	first, _ := sel.Select()
	second, _ := sel.Select()

	if first != "http://p1.local:8080" {
		t.Errorf("first Select = %q, want p1", first)
	}
	if second != "http://p2.local:8080" {
		t.Errorf("second Select = %q, want p2", second)
	}
}

func TestNewSelector_ConflictError(t *testing.T) {
	_, err := NewSelector("http://p.local", "", "proxies.txt", "sticky")
	if err == nil {
		t.Fatal("expected error when both --proxy and --proxy-file set")
	}
}

func TestNewSelector_MissingFile(t *testing.T) {
	_, err := NewSelector("", "", "/nonexistent/proxies.txt", "sticky")
	if err == nil {
		t.Fatal("expected error for missing proxy file")
	}
}

func TestSelect_Sticky(t *testing.T) {
	sel, _ := NewSelector("http://one.local:8080", "", "", "sticky")

	for i := 0; i < 5; i++ {
		got, _ := sel.Select()
		if got != "http://one.local:8080" {
			t.Errorf("iteration %d: Select = %q, want sticky result", i, got)
		}
	}
}

func TestSelect_Random(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	os.WriteFile(path, []byte("http://a.local\nhttp://b.local\nhttp://c.local\n"), 0644)

	sel, _ := NewSelector("", "", path, "random")

	// Just verify it returns valid proxies (randomness is hard to test deterministically).
	valid := map[string]bool{
		"http://a.local": true,
		"http://b.local": true,
		"http://c.local": true,
	}
	for i := 0; i < 10; i++ {
		got, err := sel.Select()
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if !valid[got] {
			t.Errorf("Select returned invalid proxy %q", got)
		}
	}
}

func TestSelect_RoundRobin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	os.WriteFile(path, []byte("http://a.local\nhttp://b.local\nhttp://c.local\n"), 0644)

	sel, _ := NewSelector("", "", path, "round-robin")

	expected := []string{"http://a.local", "http://b.local", "http://c.local", "http://a.local"}
	for i, want := range expected {
		got, _ := sel.Select()
		if got != want {
			t.Errorf("iteration %d: Select = %q, want %q", i, got, want)
		}
	}
}

func TestSelect_Empty(t *testing.T) {
	sel, _ := NewSelector("", "", "", "sticky")
	got, err := sel.Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "" {
		t.Errorf("Select = %q, want empty", got)
	}
}

func TestSelect_NilSelector(t *testing.T) {
	var sel *Selector
	got, err := sel.Select()
	if err != nil {
		t.Fatalf("Select on nil: %v", err)
	}
	if got != "" {
		t.Errorf("Select on nil = %q, want empty", got)
	}
}

func TestWithAuth(t *testing.T) {
	tests := []struct {
		name     string
		proxy    string
		auth     string
		expected string
	}{
		{"adds auth", "http://proxy.local:8080", "user:pass", "http://user:pass@proxy.local:8080"},
		{"no auth", "http://proxy.local:8080", "", "http://proxy.local:8080"},
		{"existing userinfo preserved", "http://existing:cred@proxy.local:8080", "user:pass", "http://existing:cred@proxy.local:8080"},
		{"bad auth format", "http://proxy.local:8080", "nouserpass", "http://proxy.local:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withAuth(tt.proxy, tt.auth)
			if got != tt.expected {
				t.Errorf("withAuth(%q, %q) = %q, want %q", tt.proxy, tt.auth, got, tt.expected)
			}
		})
	}
}

func TestNormalizedStrategy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "sticky"},
		{"sticky", "sticky"},
		{"STICKY", "sticky"},
		{"random", "random"},
		{"RANDOM", "random"},
		{"round-robin", "round-robin"},
		{"Round-Robin", "round-robin"},
		{"unknown", "sticky"},
		{"  sticky  ", "sticky"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizedStrategy(tt.input)
			if got != tt.want {
				t.Errorf("normalizedStrategy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewSelector_WithAuth(t *testing.T) {
	sel, err := NewSelector("http://proxy.local:8080", "user:pass", "", "sticky")
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	got, _ := sel.Select()
	if got != "http://user:pass@proxy.local:8080" {
		t.Errorf("Select = %q, want auth injected", got)
	}
}

func TestNewSelector_FileWithAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	os.WriteFile(path, []byte("http://p1.local\nhttp://p2.local\n"), 0644)

	sel, err := NewSelector("", "u:p", path, "round-robin")
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	first, _ := sel.Select()
	if first != "http://u:p@p1.local" {
		t.Errorf("first = %q, want auth injected", first)
	}
}
