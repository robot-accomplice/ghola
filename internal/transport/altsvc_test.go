package transport

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

func uri(t *testing.T, s string) *fasthttp.URI {
	t.Helper()
	u := &fasthttp.URI{}
	if err := u.Parse(nil, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return u
}

// The cache keeps an origin's Alt-Svc in curl's file format, so curl and
// ghola can share one file. A new header replaces the origin's entries,
// "clear" and ma=0 end them, and only an h3 entry on the origin's own host
// and port sends the origin to HTTP/3 (decision K).
func TestAltSvcCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "altsvc.txt")
	c, err := loadAltSvc(path)
	if err != nil {
		t.Fatal(err)
	}
	a, b := uri(t, "https://a.example/x"), uri(t, "https://b.example:8443/")
	if err := c.record(a, "h2", `h3=":443"; ma=3600, h2="alt.example:443"`); err != nil {
		t.Fatal(err)
	}
	if err := c.record(b, "h1", `h3=":9443"`); err != nil {
		t.Fatal(err)
	}
	if err := c.record(uri(t, "http://c.example/"), "h1", `h3=":443"`); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "h2 a.example 443 h3 a.example 443 \"") || strings.Contains(string(data), "c.example") {
		t.Errorf("file:\n%s", data)
	}
	reloaded, err := loadAltSvc(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.entries) != 3 {
		t.Errorf("%d entries after a reload, want 3", len(reloaded.entries))
	}
	if !reloaded.h3(a) {
		t.Error("a.example has an h3 entry on its own port")
	}
	if reloaded.h3(b) {
		t.Error("b.example's h3 is on another port, which the HTTP/3 stack cannot reach")
	}
	if err := reloaded.record(a, "h2", "clear"); err != nil || reloaded.h3(a) {
		t.Errorf("clear kept the entry (%v)", err)
	}
	if err := reloaded.record(a, "h2", `h3=":443"; ma=0`); err != nil || reloaded.h3(a) {
		t.Errorf("ma=0 kept the entry (%v)", err)
	}
	if err := os.WriteFile(path, []byte("h2 a.example 443 h3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAltSvc(path); err == nil {
		t.Error("a bad line was accepted")
	}
}

type namedTransport struct{ name string }

func (n namedTransport) Do(context.Context, *fasthttp.Request, *fasthttp.Response) error { return nil }
func (n namedTransport) Stream(context.Context, *fasthttp.Request, io.Writer) (StreamMeta, error) {
	return StreamMeta{}, nil
}
func (n namedTransport) Name() string { return n.name }

// An origin with an h3 entry goes to the HTTP/3 stack, every other origin
// to the normal one.
func TestAltSvc_RoutesToH3(t *testing.T) {
	c := &altSvcCache{path: filepath.Join(t.TempDir(), "a")}
	if err := c.record(uri(t, "https://a.example/"), "h2", `h3=":443"`); err != nil {
		t.Fatal(err)
	}
	pt := protocolTransport{network: namedTransport{"tcp"}, h3: namedTransport{"h3"}, altsvc: c, opts: &config.Options{}}
	for u, want := range map[string]string{"https://a.example/p": "h3", "https://other.example/": "tcp", "http://a.example/": "tcp"} {
		if got := pt.networkFor(uri(t, u)).Name(); got != want {
			t.Errorf("%s went to %s, want %s", u, got, want)
		}
	}
}

// Without a stack that has HTTP/3, --alt-svc keeps the cache and does not
// change the protocol.
func TestAltSvc_NoH3StackOnTheDefaultStack(t *testing.T) {
	for name, opts := range map[string]*config.Options{
		"default stack": {},
		"with a proxy":  {Stealth: config.StealthOptions{Impersonate: "chrome"}},
	} {
		proxy := ""
		if name == "with a proxy" {
			proxy = "http://127.0.0.1:1"
		}
		h3, err := newH3Network(opts, proxy)
		if err != nil || h3 != nil {
			t.Errorf("%s: %v %v, want no HTTP/3 stack", name, h3, err)
		}
	}
	h3, err := newH3Network(&config.Options{Stealth: config.StealthOptions{Impersonate: "chrome"}}, "")
	if err != nil || h3 == nil {
		t.Errorf("chrome: %v %v, want an HTTP/3 stack", h3, err)
	}
}
