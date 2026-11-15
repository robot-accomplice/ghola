//go:build bench

// Package bench measures ghola's three HTTP stacks on the same jobs, for
// decision D3 in docs/plans/2026-11-15-parity-and-ux-plan.md (one engine or
// more). Run it with:
//
//	go test -tags bench -bench . -benchmem -count 5 ./test/bench/
//
// The server runs in the same process, so absolute numbers are lower than on
// a real network. The comparison between the stacks is what counts.
package bench

import (
	"bytes"
	"crypto/tls"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/valyala/fasthttp"
)

const (
	bigSize   = 256 << 20 // a large download
	smallSize = 1 << 10   // a small API answer
)

var modTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func payload(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(int64(n))).Read(b)
	return b
}

var (
	big   = payload(bigSize)
	small = payload(smallSize)
)

func newServers(b *testing.B) (plain, secure *httptest.Server) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := small
		if r.URL.Path == "/big" {
			body = big
		}
		http.ServeContent(w, r, "f", modTime, bytes.NewReader(body))
	})
	plain, secure = httptest.NewServer(h), httptest.NewTLSServer(h)
	b.Cleanup(func() { plain.Close(); secure.Close() })
	return plain, secure
}

// fetcher downloads url and writes the body to io.Discard. It returns the
// number of body bytes.
type fetcher func(url string) (int64, error)

func fasthttpFetcher() fetcher {
	c := &fasthttp.Client{
		TLSConfig:           &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
		MaxResponseBodySize: bigSize + 1,
	}
	return func(url string) (int64, error) {
		req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(rsp)
		req.SetRequestURI(url)
		if err := c.Do(req, rsp); err != nil {
			return 0, err
		}
		n, err := io.Copy(io.Discard, bytes.NewReader(rsp.Body()))
		return n, err
	}
}

func netHTTPFetcher() fetcher {
	c := &http.Client{Transport: &http.Transport{
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
		DisableCompression: true,
	}}
	return func(url string) (int64, error) {
		resp, err := c.Get(url)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(io.Discard, resp.Body)
	}
}

func tlsClientFetcher(b *testing.B) fetcher {
	c, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profiles.MappedTLSClients["chrome_146"]), // the profile ghola uses for --impersonate chrome
		tlsclient.WithInsecureSkipVerify(),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithDisableHttp3(),
	)
	if err != nil {
		b.Fatal(err)
	}
	return func(url string) (int64, error) {
		req, err := fhttp.NewRequest(fhttp.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(io.Discard, resp.Body)
	}
}

func run(b *testing.B, f fetcher, url string, size int) {
	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		n, err := f(url)
		if err != nil {
			b.Fatal(err)
		}
		if n != int64(size) {
			b.Fatalf("got %d bytes, want %d", n, size)
		}
	}
}

func BenchmarkEngines(b *testing.B) {
	plain, secure := newServers(b)
	engines := []struct {
		name string
		make func(*testing.B) fetcher
	}{
		{"fasthttp", func(*testing.B) fetcher { return fasthttpFetcher() }},
		{"nethttp", func(*testing.B) fetcher { return netHTTPFetcher() }},
		{"tlsclient", tlsClientFetcher},
	}
	jobs := []struct {
		name string
		url  string
		size int
	}{
		{"http-256MiB", plain.URL + "/big", bigSize},
		{"https-256MiB", secure.URL + "/big", bigSize},
		{"http-1KiB", plain.URL + "/small", smallSize},
		{"https-1KiB", secure.URL + "/small", smallSize},
	}
	for _, job := range jobs {
		for _, e := range engines {
			b.Run(job.name+"/"+e.name, func(b *testing.B) { run(b, e.make(b), job.url, job.size) })
		}
	}
}
