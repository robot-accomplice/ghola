package transport

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

func ftpDo(t *testing.T, opts *config.Options, proxy, method, url, rng string, body []byte) (*fasthttp.Response, error) {
	t.Helper()
	tr, err := New(opts, proxy)
	if err != nil {
		t.Fatal(err)
	}
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(url)
	req.Header.SetMethod(method)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	req.SetBody(body)
	rsp := fasthttp.AcquireResponse()
	t.Cleanup(func() { fasthttp.ReleaseResponse(rsp) })
	err = tr.Do(context.Background(), req, rsp)
	return rsp, err
}

func readFTP(t *testing.T, rsp *fasthttp.Response) string {
	t.Helper()
	b, err := ReadBody(rsp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

var ftpFiles = func() map[string][]byte {
	return map[string][]byte{"pub/a.txt": []byte("0123456789"), "pub/b.txt": []byte("b")}
}

// An ftp:// URL downloads the file with an anonymous login, as curl does,
// with the size and the time of the file as headers.
func TestFTP_DownloadsAnonymously(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	rsp, err := ftpDo(t, &config.Options{}, "", "GET", "ftp://"+srv.addr()+"/pub/a.txt", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFTP(t, rsp); got != "0123456789" || rsp.StatusCode() != 200 {
		t.Fatalf("status %d, body %q", rsp.StatusCode(), got)
	}
	if !srv.saw("USER anonymous") || len(rsp.Header.Peek("Last-Modified")) == 0 {
		t.Error("no anonymous login, or no Last-Modified")
	}
}

// A range uses REST, so -C - resumes an FTP download.
func TestFTP_RangeUsesREST(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	rsp, err := ftpDo(t, &config.Options{}, "", "GET", "ftp://"+srv.addr()+"/pub/a.txt", "bytes=4-", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFTP(t, rsp); got != "456789" || rsp.StatusCode() != 206 || string(rsp.Header.Peek("Content-Range")) != "bytes 4-9/10" {
		t.Fatalf("status %d, body %q, Content-Range %q", rsp.StatusCode(), got, rsp.Header.Peek("Content-Range"))
	}
	if !srv.saw("REST 4") {
		t.Error("no REST 4")
	}
}

// A path that ends in / lists the folder.
func TestFTP_ListsAFolder(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	rsp, err := ftpDo(t, &config.Options{}, "", "GET", "ftp://"+srv.addr()+"/pub/", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFTP(t, rsp); got != "a.txt\nb.txt\n" {
		t.Errorf("listing = %q", got)
	}
}

// -T (PUT) stores the file.
func TestFTP_Upload(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	if _, err := ftpDo(t, &config.Options{}, "", "PUT", "ftp://"+srv.addr()+"/pub/new.txt", "", []byte("up")); err != nil {
		t.Fatal(err)
	}
	if b, _ := srv.file("pub/new.txt"); string(b) != "up" {
		t.Errorf("stored %q", b)
	}
}

// -u wins over the URL, and a refused login is ErrFTPLogin (exit 67).
func TestFTP_Login(t *testing.T) {
	srv := newFTPServer(t, "alice:s3cret", ftpFiles())
	url := "ftp://bob:wrong@" + srv.addr() + "/pub/a.txt"
	if _, err := ftpDo(t, &config.Options{}, "", "GET", url, "", nil); !errors.Is(err, ErrFTPLogin) {
		t.Fatalf("wrong URL credentials: err = %v, want ErrFTPLogin", err)
	}
	good := "ftp://alice:s3cret@" + srv.addr() + "/pub/a.txt" // fasthttp drops the URL user unless ghola keeps it
	if _, err := ftpDo(t, &config.Options{}, "", "GET", good, "", nil); err != nil {
		t.Fatalf("URL credentials: %v", err)
	}
	if _, err := ftpDo(t, &config.Options{User: "alice:s3cret"}, "", "GET", url, "", nil); err != nil {
		t.Fatalf("-u alice: %v", err)
	}
}

// A missing file is ErrFTPNotFound (exit 78).
func TestFTP_MissingFile(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	if _, err := ftpDo(t, &config.Options{}, "", "GET", "ftp://"+srv.addr()+"/pub/none", "", nil); !errors.Is(err, ErrFTPNotFound) {
		t.Fatalf("err = %v, want ErrFTPNotFound", err)
	}
}

// A SOCKS proxy carries the control and the data connections.
func TestFTP_ThroughSOCKS(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	socks := newSocksServer(t, "")
	rsp, err := ftpDo(t, &config.Options{}, "socks5h://"+socks.ln.Addr().String(), "GET", "ftp://"+srv.addr()+"/pub/a.txt", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFTP(t, rsp); got != "0123456789" {
		t.Fatalf("body %q", got)
	}
	socks.mu.Lock()
	n := len(socks.versions)
	socks.mu.Unlock()
	if n < 2 {
		t.Errorf("the proxy saw %d connections, want the control and the data connection", n)
	}
}

// An HTTP proxy cannot carry FTP. ghola refuses, because going direct
// without a word would show the user's address to the server.
func TestFTP_HTTPProxyRefused(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	_, err := ftpDo(t, &config.Options{}, "http://127.0.0.1:1", "GET", "ftp://"+srv.addr()+"/pub/a.txt", "", nil)
	if !errors.Is(err, ErrFTPProxy) {
		t.Fatalf("err = %v, want ErrFTPProxy", err)
	}
	if srv.saw("USER") {
		t.Error("ghola connected to the server directly")
	}
}

// ftps:// is implicit TLS on the control and the data connections, with
// the TLS options of HTTPS.
func TestFTPS_Implicit(t *testing.T) {
	cert := httptest.NewTLSServer(nil)
	defer cert.Close()
	srv := newFTPServerTLS(t, "", ftpFiles(), cert.TLS)
	opts := &config.Options{Stealth: config.StealthOptions{Insecure: true}}
	rsp, err := ftpDo(t, opts, "", "GET", "ftps://"+srv.addr()+"/pub/a.txt", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFTP(t, rsp); got != "0123456789" {
		t.Fatalf("body %q", got)
	}
	if _, err := ftpDo(t, &config.Options{}, "", "GET", "ftps://"+srv.addr()+"/pub/a.txt", "", nil); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("a self-signed certificate was accepted without -k: %v", err)
	}
}
