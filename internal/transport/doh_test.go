package transport

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/robot-accomplice/ghola/internal/config"
)

// dohServer answers A questions for ghola-test.invalid with 127.0.0.1,
// and every other name with no answer. It counts the questions.
func dohServer(t *testing.T, asked *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p dnsmessage.Parser
		h, err := p.Start(body)
		if err != nil || r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		q, _ := p.Question()
		asked.Add(1)
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true})
		b.StartQuestions() //nolint:errcheck
		b.Question(q)      //nolint:errcheck
		b.StartAnswers()   //nolint:errcheck
		if q.Name.String() == "ghola-test.invalid." && q.Type == dnsmessage.TypeA {
			b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}) //nolint:errcheck
		}
		msg, _ := b.Finish()
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(msg) //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	return ts
}

// --doh resolves the name with DNS over HTTPS, on both stacks: the name
// does not resolve in the system, so only the DoH answer can make the
// request work. A name without an answer exits 6, as in curl. With
// --ip 4, ghola asks only for A.
func TestDoH(t *testing.T) {
	var asked atomic.Int32
	doh := dohServer(t, &asked)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			asked.Store(0)
			tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: config.NetOptions{DoH: doh.URL, IPVersion: "4"}}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := get(t, tr, "http://ghola-test.invalid:"+port(target)+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			if n := asked.Load(); n != 1 {
				t.Errorf("%d DoH questions; want 1 (A only, with --ip 4)", n)
			}
			if err := get(t, tr, "http://other.invalid:"+port(target)+"/"); !errors.Is(err, ErrNoAddress) {
				t.Errorf("a name without an answer: err = %v", err)
			}
		})
	}
}
