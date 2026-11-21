package transport

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/robot-accomplice/ghola/internal/config"
)

// echKey builds one ECH config (draft-ietf-tls-esni-22, X25519 with
// HKDF-SHA256 and AES-128-GCM) and its private key.
func echKey(t *testing.T, publicName string) (config []byte, key []byte) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := k.PublicKey().Bytes()
	var c []byte
	c = append(c, 1)                             // config_id
	c = binary.BigEndian.AppendUint16(c, 0x0020) // KEM X25519
	c = binary.BigEndian.AppendUint16(c, uint16(len(pub)))
	c = append(c, pub...)
	c = binary.BigEndian.AppendUint16(c, 4)      // cipher suites: 4 bytes
	c = binary.BigEndian.AppendUint16(c, 0x0001) // HKDF-SHA256
	c = binary.BigEndian.AppendUint16(c, 0x0001) // AES-128-GCM
	c = append(c, 0, byte(len(publicName)))      // maximum_name_length, public_name length
	c = append(c, publicName...)
	c = binary.BigEndian.AppendUint16(c, 0) // no extensions
	cfg := binary.BigEndian.AppendUint16(nil, 0xfe0d)
	cfg = binary.BigEndian.AppendUint16(cfg, uint16(len(c)))
	return append(cfg, c...), k.Bytes()
}

// echDoH answers A with 127.0.0.1, and HTTPS with the ECH config list.
func echDoH(t *testing.T, list []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p dnsmessage.Parser
		if _, err := p.Start(body); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		q, _ := p.Question()
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true})
		b.StartQuestions() //nolint:errcheck
		b.Question(q)      //nolint:errcheck
		b.StartAnswers()   //nolint:errcheck
		h := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
		switch q.Type {
		case dnsmessage.TypeA:
			b.AResource(h, dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}) //nolint:errcheck
		case dnsmessage.TypeHTTPS:
			if list != nil {
				target, _ := dnsmessage.NewName(".")
				b.HTTPSResource(h, dnsmessage.HTTPSResource{SVCBResource: dnsmessage.SVCBResource{ //nolint:errcheck
					Priority: 1, Target: target, Params: []dnsmessage.SVCParam{{Key: dnsmessage.SVCParamECH, Value: list}},
				}})
			}
		}
		msg, _ := b.Finish()
		w.Write(msg) //nolint:errcheck
	}))
	t.Cleanup(ts.Close)
	return ts
}

// --tls-ech encrypts the Client Hello when the host's HTTPS record offers
// an ECH config, so the network does not see the server name. A host
// without ECH gets plain TLS. Without --doh the option is an error.
func TestTLS_ECH(t *testing.T) {
	cfg, key := echKey(t, "public.invalid")
	var accepted atomic.Bool
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		accepted.Store(r.TLS.ECHAccepted)
	}))
	ts.TLS = &tls.Config{EncryptedClientHelloKeys: []tls.EncryptedClientHelloKey{{Config: cfg, PrivateKey: key}}}
	ts.StartTLS()
	defer ts.Close()
	list := binary.BigEndian.AppendUint16(nil, uint16(len(cfg)))
	list = append(list, cfg...)
	url := "https://ghola-test.invalid:" + port(ts) + "/"
	for name, tc := range map[string]struct {
		list []byte
		want bool
	}{"ECH offered": {list, true}, "no ECH": {nil, false}} {
		accepted.Store(false)
		doh := echDoH(t, tc.list)
		tr, err := New(&config.Options{Stealth: config.StealthOptions{ECH: true, Insecure: true}, Net: config.NetOptions{DoH: doh.URL}}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := get(t, tr, url); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if accepted.Load() != tc.want {
			t.Errorf("%s: the server accepted ECH: %v", name, accepted.Load())
		}
	}
	if _, err := New(&config.Options{Stealth: config.StealthOptions{ECH: true}}, ""); !errors.Is(err, ErrECHNeedsDoH) {
		t.Errorf("without --doh: err = %v", err)
	}
}
