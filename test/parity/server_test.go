//go:build parity

package parity

// The test server serves deterministic content and the hostile cases from
// the 2026-11-12 audit. Each tool run uses its own path prefix /s/<key>/, so
// the server keeps a separate request log and separate counters for each run.

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// reqLog is one request as the server saw it. It is the record of what a
// tool sent on the wire.
type reqLog struct {
	Method string      `json:"method"`
	Host   string      `json:"host"`
	Path   string      `json:"path"`
	Header http.Header `json:"header"`
	Body   string      `json:"body,omitempty"`
}

type server struct {
	plain, tls *httptest.Server
	mux        *http.ServeMux
	caFile     string
	deadURL    string // a local port with no listener
	// silent accepts TCP connections and never sends a byte, so a TLS
	// handshake to silentURL never ends.
	silent    net.Listener
	silentURL string
	silentMu  sync.Mutex
	silentCs  []net.Conn

	mu    sync.Mutex
	logs  map[string][]reqLog
	hits  map[string]int
	cache map[int][]byte
}

type keyCtx struct{}

var modTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// gzipText is the decoded body of /gzip.
var gzipText = strings.Repeat("compressible line\n", 200) + "\n"

func newServer(dir string) (*server, error) {
	s := &server{logs: map[string][]reqLog{}, hits: map[string]int{}, cache: map[int][]byte{}}
	s.routes()
	s.plain = httptest.NewServer(s)
	s.tls = httptest.NewTLSServer(s)

	s.caFile = filepath.Join(dir, "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.tls.Certificate().Raw})
	if err := os.WriteFile(s.caFile, pemBytes, 0o644); err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.deadURL = "http://" + ln.Addr().String() + "/"
	ln.Close()

	if s.silent, err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
		return nil, err
	}
	s.silentURL = "https://" + s.silent.Addr().String() + "/"
	go s.holdSilent()
	return s, nil
}

// holdSilent accepts connections on s.silent and keeps them open until close.
func (s *server) holdSilent() {
	for {
		c, err := s.silent.Accept()
		if err != nil {
			return
		}
		s.silentMu.Lock()
		s.silentCs = append(s.silentCs, c)
		s.silentMu.Unlock()
	}
}

func (s *server) close() {
	s.silent.Close()
	s.silentMu.Lock()
	for _, c := range s.silentCs {
		c.Close()
	}
	s.silentMu.Unlock()
	s.plain.CloseClientConnections()
	s.tls.CloseClientConnections()
	s.plain.Close()
	s.tls.Close()
}

// content returns n deterministic bytes. The same n always gives the same bytes.
func (s *server) content(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.cache[n]; ok {
		return b
	}
	b := make([]byte, n)
	rand.New(rand.NewSource(int64(n))).Read(b)
	s.cache[n] = b
	return b
}

func (s *server) hit(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[key]++
	return s.hits[key]
}

func (s *server) requests(key string) []reqLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reqLog(nil), s.logs[key]...)
}

// ServeHTTP logs the request under its run key, then serves the path after
// the /s/<key> prefix.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/s/")
	key, sub, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	s.mu.Lock()
	s.logs[key] = append(s.logs[key], reqLog{
		Method: r.Method, Host: r.Host, Path: "/" + sub, Header: r.Header.Clone(), Body: string(body),
	})
	s.mu.Unlock()

	r2 := r.Clone(context.WithValue(r.Context(), keyCtx{}, key))
	r2.URL.Path = "/" + sub
	r2.URL.RawPath = ""
	s.mux.ServeHTTP(w, r2)
}

func runKey(r *http.Request) string { return r.Context().Value(keyCtx{}).(string) }

// redirect sends a redirect to path inside the same run prefix.
func redirect(w http.ResponseWriter, r *http.Request, path string, code int) {
	http.Redirect(w, r, "/s/"+runKey(r)+path, code)
}

func parseSize(s string) int {
	mult := 1
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1<<10, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1<<20, strings.TrimSuffix(s, "m")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 1024
	}
	return n * mult
}

func (s *server) routes() {
	m := http.NewServeMux()
	s.mux = m
	size := func(r *http.Request) []byte { return s.content(parseSize(r.PathValue("size"))) }

	// A normal file with Range support, ETag and Last-Modified.
	m.HandleFunc("/file/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(b)))
		http.ServeContent(w, r, "file.bin", modTime, bytes.NewReader(b))
	})
	// A body without Content-Length (chunked).
	m.HandleFunc("/chunked/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		for i := 0; i < len(b); i += 4096 {
			_, _ = w.Write(b[i:min(i+4096, len(b))])
			w.(http.Flusher).Flush()
		}
	})
	// No Range support: always 200 and the full body.
	m.HandleFunc("/norange/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	})
	// Says Accept-Ranges, but answers a range with 200 and the full body.
	m.HandleFunc("/liar/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	})
	// Answers a range with 206, but the Content-Range starts at 0.
	m.HandleFunc("/badrange/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(b)-1, len(b)))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(b)
	})
	// The content, ETag and Last-Modified change on every request.
	m.HandleFunc("/changing/{size}", func(w http.ResponseWriter, r *http.Request) {
		n := s.hit("changing-" + runKey(r))
		b := changingVersion(s, parseSize(r.PathValue("size")), n)
		w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, n))
		http.ServeContent(w, r, "f", modTime.Add(time.Duration(n)*time.Hour), bytes.NewReader(b))
	})
	// Promises the full Content-Length, sends half, and closes the connection.
	m.HandleFunc("/truncate/{size}", func(w http.ResponseWriter, r *http.Request) {
		b := size(r)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b[:len(b)/2])
		w.(http.Flusher).Flush()
		if hj, ok := w.(http.Hijacker); ok {
			if c, _, err := hj.Hijack(); err == nil {
				c.Close()
			}
		}
	})
	// Never answers.
	m.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	// One byte each 500 ms, for at most 60 s.
	m.HandleFunc("/trickle", func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 120; i++ {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	})
	m.HandleFunc("/status/{code}", func(w http.ResponseWriter, r *http.Request) {
		c, _ := strconv.Atoi(r.PathValue("code"))
		w.WriteHeader(c)
		fmt.Fprintf(w, "status %d body\n", c)
	})
	// 503 for the first 2 requests, then 200.
	m.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		if s.hit("flaky-"+runKey(r)) <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "busy")
			return
		}
		fmt.Fprintln(w, "ok after retries")
	})
	// A chain of n redirects, then /file/1k.
	m.HandleFunc("/redirect/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.PathValue("n"))
		if n <= 1 {
			redirect(w, r, "/file/1k", http.StatusFound)
			return
		}
		redirect(w, r, fmt.Sprintf("/redirect/%d", n-1), http.StatusFound)
	})
	// A redirect to another host name (localhost) on the same server. The
	// target uses the server's own port, not the request's Host header.
	m.HandleFunc("/xhost", func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(s.plain.Listener.Addr().String())
		http.Redirect(w, r, "http://localhost:"+port+"/s/"+url.PathEscape(runKey(r))+"/echo", http.StatusFound)
	})
	m.HandleFunc("/r307", func(w http.ResponseWriter, r *http.Request) {
		redirect(w, r, "/echo", http.StatusTemporaryRedirect)
	})
	m.HandleFunc("/r303", func(w http.ResponseWriter, r *http.Request) {
		redirect(w, r, "/echo", http.StatusSeeOther)
	})
	m.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "echo") })
	m.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/plain")
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, gzipText)
		gz.Close()
	})
	m.HandleFunc("/cd-evil", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../evil.txt"`)
		fmt.Fprintln(w, "evil")
	})
	m.HandleFunc("/basic", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="parity"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, "authorized")
	})
}

// changingVersion is the content that /changing serves on its n-th request.
func changingVersion(s *server, size, n int) []byte { return s.content(size + n)[:size] }
