package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// Do returns when the headers arrive and leaves the body as a stream in rsp.
// Before, Do read the whole body into memory, so stdout needed memory for
// the whole file (audit finding M2). The server here holds the rest of the
// body until Do returns: a buffering Do never returns.
func TestDo_ReturnsBeforeTheBodyEnds(t *testing.T) {
	for _, impersonate := range []string{"", "chrome"} {
		t.Run("impersonate="+impersonate, func(t *testing.T) {
			release := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "10")
				io.WriteString(w, "first") //nolint:errcheck
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				io.WriteString(w, "-last") //nolint:errcheck
			}))
			defer srv.Close()

			tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: impersonate}}, "")
			if err != nil {
				t.Fatal(err)
			}
			req := fasthttp.AcquireRequest()
			rsp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseRequest(req)
			defer fasthttp.ReleaseResponse(rsp)
			req.SetRequestURI(srv.URL + "/")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- tr.Do(ctx, req, rsp) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Do: %v", err)
				}
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("Do did not return before the body ended: it buffers the body")
			}
			close(release)

			if rsp.StatusCode() != 200 {
				t.Errorf("status %d, want 200", rsp.StatusCode())
			}
			// tls-client removes the server's Content-Length header, so only
			// net/http can report it.
			if impersonate == "" && rsp.Header.ContentLength() != 10 {
				t.Errorf("Content-Length %d, want 10\n%s", rsp.Header.ContentLength(), rsp.Header.Header())
			}
			body, err := ReadBody(rsp)
			if err != nil || string(body) != "first-last" {
				t.Errorf("ReadBody = %q, %v; want \"first-last\"", body, err)
			}
		})
	}
}

// ReadBody reports a body that breaks off. fasthttp's Response.Body() puts
// the error text in place of the body instead, so a caller that uses it gets
// wrong data and no error.
func TestReadBody_ReportsABrokenBody(t *testing.T) {
	broken := errors.New("connection reset")
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	rsp.SetBodyStream(io.MultiReader(bytes.NewReader([]byte("part")), errReader{broken}), -1)

	if _, err := ReadBody(rsp); !errors.Is(err, broken) {
		t.Fatalf("err = %v, want %v", err, broken)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
