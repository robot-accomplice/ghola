package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// ErrFileRead marks a file:// URL whose file ghola could not read. curl
// exits 37 ("Couldn't read a file:// file").
var ErrFileRead = errors.New("could not read the file:// file")

// SchemeFile is the scheme of a local file URL (plan phase 5, decision D1).
const SchemeFile = "file"

// protocolTransport serves file://, ftp(s)://, sftp:// and scp:// URLs itself and sends
// every other URL to the network transport. It sits in front of both
// stacks, so stdout and -o, and both stacks, handle these protocols the
// same way. Their answers look like HTTP answers (200, 206, Content-Length,
// Last-Modified), so the range checks, resume and -R work unchanged.
type protocolTransport struct {
	network Transport
	opts    *config.Options
	proxy   string // the --proxy URL, "" for the environment
}

// serve answers req when its protocol is not HTTP. ok is false for HTTP.
func (t protocolTransport) serve(ctx context.Context, req *fasthttp.Request) (a fileAnswer, ok bool, err error) {
	switch strings.ToLower(string(req.URI().Scheme())) {
	case SchemeFile:
		a, err = serveFile(req)
		return a, true, err
	case SchemeFTP, SchemeFTPS:
		a, err = t.serveFTP(ctx, req)
		return a, true, err
	case SchemeSFTP:
		a, err = t.serveSFTP(ctx, req)
		return a, true, err
	case SchemeSCP:
		a, err = t.serveSCP(ctx, req)
		return a, true, err
	}
	return fileAnswer{}, false, nil
}

func (t protocolTransport) Name() string { return t.network.Name() }

func (t protocolTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	a, ok, err := t.serve(ctx, req)
	if !ok {
		return t.network.Do(ctx, req, rsp)
	}
	if err != nil {
		return err
	}
	fillResponse(rsp, a.status, a.header, a.body, a.length)
	return nil
}

func (t protocolTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	a, ok, err := t.serve(ctx, req)
	if !ok {
		return t.network.Stream(ctx, req, sink)
	}
	if err != nil {
		return StreamMeta{}, err
	}
	defer a.body.Close()
	meta := NewStreamMeta(a.status, a.length, a.header.Get, nil)
	return meta, CopyBody(sink, meta, a.body)
}

// fileAnswer is the HTTP-like answer to a file:// request.
type fileAnswer struct {
	status int
	header http.Header
	body   io.ReadCloser
	length int64
}

// serveFile answers GET and HEAD, with a byte Range, and PUT (an upload
// with -T writes the file, as in curl).
func serveFile(req *fasthttp.Request) (fileAnswer, error) {
	u, err := url.Parse(req.URI().String())
	if err != nil {
		return fileAnswer{}, err
	}
	path, err := localPath(u)
	if err != nil {
		return fileAnswer{}, err
	}
	switch method := string(req.Header.Method()); method {
	case fasthttp.MethodGet, fasthttp.MethodHead:
		return readFile(path, string(req.Header.Peek("Range")), method == fasthttp.MethodHead)
	case fasthttp.MethodPut:
		if err := os.WriteFile(path, req.Body(), 0o644); err != nil {
			return fileAnswer{}, fmt.Errorf("write %s: %w", path, err)
		}
		return emptyAnswer(), nil
	default:
		return fileAnswer{}, fmt.Errorf("file://: the %s method is not supported", method)
	}
}

// localPath returns the local path of a file:// URL. The host must be empty
// or localhost. On Windows, file:///C:/dir/f is C:\dir\f.
func localPath(u *url.URL) (string, error) {
	if h := strings.ToLower(u.Hostname()); h != "" && h != "localhost" {
		return "", fmt.Errorf("file://: the host %q is not this computer", u.Host)
	}
	p := u.Path
	if p == "" {
		return "", errors.New("file://: the URL has no path")
	}
	if runtime.GOOS == "windows" {
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		p = filepath.FromSlash(p)
	}
	return p, nil
}

func readFile(path, rangeHeader string, head bool) (fileAnswer, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileAnswer{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fileAnswer{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	if info.IsDir() {
		defer f.Close()
		return listDir(f, head)
	}
	a, start, ok := rangeAnswer(info.Size(), info.ModTime(), rangeHeader)
	if !ok || head {
		f.Close()
		return a, nil
	}
	a.body = sectionCloser{io.NewSectionReader(f, start, a.length), f}
	return a, nil
}

// parseRange reads "bytes=a-", "bytes=a-b" and "bytes=-n" for a file of
// size bytes. It returns false for a range that the file cannot satisfy.
func parseRange(header string, size int64) (start, length int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	first, last, dash := strings.Cut(spec, "-")
	if !found || !dash || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	if first == "" { // the last n bytes
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		n = min(n, size)
		return size - n, n, size > 0
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
			return 0, 0, false
		}
		end = min(end, size-1)
	}
	return start, end - start + 1, true
}

// listDir answers a folder with its names, one for each line, as curl does.
func listDir(f *os.File, head bool) (fileAnswer, error) {
	names, err := f.Readdirnames(-1)
	if err != nil {
		return fileAnswer{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	sort.Strings(names)
	return listAnswer(names, head), nil
}

// sectionCloser reads a part of a file and closes the file.
type sectionCloser struct {
	io.Reader
	f *os.File
}

func (s sectionCloser) Close() error { return s.f.Close() }

// requestURL returns the URL of req with its user and password.
// fasthttp's URI.String drops them, so ftp://user:pass@host lost its login.
func requestURL(req *fasthttp.Request) (*url.URL, error) {
	u, err := url.Parse(req.URI().String())
	if err != nil {
		return nil, err
	}
	if name := string(req.URI().Username()); name != "" {
		if pass := req.URI().Password(); len(pass) > 0 {
			u.User = url.UserPassword(name, string(pass))
		} else {
			u.User = url.User(name)
		}
	}
	return u, nil
}
