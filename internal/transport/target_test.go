package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// --url-path-as-is keeps ./ and ../ in the path, and --request-target
// replaces the request target, on both stacks. Both exist to test servers
// with requests that a normal client would never send, so the bytes on
// the request line must be exactly the ones asked for.
func TestRequestLine(t *testing.T) {
	var mu sync.Mutex
	var got string
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.RequestURI
		mu.Unlock()
	}))
	ts.Config.DisableGeneralOptionsHandler = true // let the handler see OPTIONS *
	ts.Start()
	defer ts.Close()
	tests := []struct {
		name, method, path string
		opts               config.Options
		want               string
	}{
		{"normalized", "GET", "/a/./b/../c?q=1", config.Options{}, "/a/c?q=1"},
		{"--url-path-as-is", "GET", "/a/./b/../c?q=1", config.Options{PathAsIs: true}, "/a/./b/../c?q=1"},
		{"--request-target", "GET", "/a?q=1", config.Options{RequestTarget: "/other?x=2"}, "/other?x=2"},
		{"--request-target *", "OPTIONS", "/", config.Options{RequestTarget: "*"}, "*"},
	}
	for _, imp := range stacks {
		for _, tc := range tests {
			t.Run(tc.name+", impersonate="+imp, func(t *testing.T) {
				opts := tc.opts
				opts.Stealth.Impersonate = imp
				tr, err := New(&opts, "")
				if err != nil {
					t.Fatal(err)
				}
				req, rsp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
				defer fasthttp.ReleaseRequest(req)
				defer fasthttp.ReleaseResponse(rsp)
				req.SetRequestURI(ts.URL + tc.path)
				req.Header.SetMethod(tc.method)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := tr.Do(ctx, req, rsp); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				defer mu.Unlock()
				if got != tc.want {
					t.Errorf("request target %q, want %q", got, tc.want)
				}
			})
		}
	}
}
