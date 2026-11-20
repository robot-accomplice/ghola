package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/valyala/fasthttp"
)

// The FTP schemes (plan phase 5, decision D1). ftps is implicit TLS, as in
// curl's ftps://.
const (
	SchemeFTP  = "ftp"
	SchemeFTPS = "ftps"
)

const (
	ftpPort  = "21"
	ftpsPort = "990"
	// curl's anonymous login.
	ftpAnonUser = "anonymous"
	ftpAnonPass = "ftp@example.com"
)

// The errors of the FTP and SSH protocols, with curl's exit codes: a refused
// login is 67 and a missing file is 78.
var (
	ErrLoginDenied    = errors.New("the server refused the login")
	ErrRemoteNotFound = errors.New("the remote file was not found")
	// ErrNoHTTPProxy refuses FTP, SFTP and SCP through an HTTP proxy. Going
	// direct without a word would show the user's address to the server; a
	// SOCKS proxy carries these protocols.
	ErrNoHTTPProxy = errors.New("this protocol does not go through an HTTP proxy; use a SOCKS proxy")
)

// serveFTP answers an ftp:// or ftps:// request: RETR for a file (SIZE and
// MDTM give the headers, REST gives a range), a name list for a path that
// ends in /, and STOR for PUT (-T).
func (t protocolTransport) serveFTP(ctx context.Context, req *fasthttp.Request) (fileAnswer, error) {
	u, err := requestURL(req)
	if err != nil {
		return fileAnswer{}, err
	}
	c, err := t.dialFTP(ctx, u)
	if err != nil {
		return fileAnswer{}, err
	}
	// The path is relative to the login folder, as in curl.
	p := strings.TrimPrefix(u.Path, "/")
	method := string(req.Header.Method())
	var a fileAnswer
	switch {
	case method == fasthttp.MethodPut:
		a, err = ftpStore(c, p, req.Body())
	case method != fasthttp.MethodGet && method != fasthttp.MethodHead:
		err = fmt.Errorf("ftp://: the %s method is not supported", method)
	case p == "" || strings.HasSuffix(p, "/"):
		a, err = ftpList(c, p, method == fasthttp.MethodHead)
	default:
		return ftpRetrieve(c, p, string(req.Header.Peek("Range")), method == fasthttp.MethodHead)
	}
	c.Quit() //nolint:errcheck
	return a, err
}

// dialFTP connects and logs in. Every connection, control and data, goes
// through ghola's dial function: directly, or through a SOCKS proxy, with
// TLS for ftps. The library gets the TLS config too, only so that it asks
// the server to protect the data connection (PBSZ and PROT P).
func (t protocolTransport) dialFTP(ctx context.Context, u *url.URL) (*ftp.ServerConn, error) {
	base, err := t.tunnelDialer(u)
	if err != nil {
		return nil, err
	}
	options := []ftp.DialOption{ftp.DialWithContext(ctx)}
	dial := func(network, addr string) (net.Conn, error) { return base(ctx, network, addr) }
	port := ftpPort
	if strings.EqualFold(u.Scheme, SchemeFTPS) {
		port = ftpsPort
		cfg, err := buildTLSConfig(t.opts)
		if err != nil {
			return nil, err
		}
		if cfg == nil {
			cfg = &tls.Config{}
		}
		cfg = cfg.Clone()
		cfg.ServerName = u.Hostname() // the data connections use the control host's name
		options = append(options, ftp.DialWithTLS(cfg))
		dial = func(network, addr string) (net.Conn, error) {
			conn, err := base(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return tls.Client(conn, cfg), nil
		}
	}
	options = append(options, ftp.DialWithDialFunc(dial))
	if u.Port() != "" {
		port = u.Port()
	}
	c, err := ftp.Dial(net.JoinHostPort(u.Hostname(), port), options...)
	if err != nil {
		return nil, err
	}
	user, pass := ftpLogin(u, t.opts.User)
	if err := c.Login(user, pass); err != nil {
		c.Quit() //nolint:errcheck
		return nil, fmt.Errorf("%w: %w", ErrLoginDenied, err)
	}
	return c, nil
}

// tunnelDialer returns the base dial function of FTP, SFTP and SCP: through the SOCKS proxy that
// --proxy or the environment names, or direct. An HTTP proxy is refused.
func (t protocolTransport) tunnelDialer(u *url.URL) (dialFunc, error) {
	direct := (&net.Dialer{}).DialContext
	var p *url.URL
	if t.proxy != "" {
		var err error
		if p, err = url.Parse(t.proxy); err != nil {
			return nil, err
		}
	} else {
		var err error
		if p, err = proxyFromEnv(u); err != nil {
			return nil, err
		}
	}
	switch {
	case p == nil:
		return direct, nil
	case isSOCKS(p):
		return socksDialer(p, direct), nil
	default:
		return nil, fmt.Errorf("%w (proxy %s)", ErrNoHTTPProxy, p.Redacted())
	}
}

// ftpLogin returns the user and password: -u wins over the URL, and with
// neither, the anonymous login of curl.
func ftpLogin(u *url.URL, cliUser string) (string, string) {
	if cliUser != "" {
		user, pass, _ := strings.Cut(cliUser, ":")
		return user, pass
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		return u.User.Username(), pass
	}
	return ftpAnonUser, ftpAnonPass
}

func ftpRetrieve(c *ftp.ServerConn, p, rangeHeader string, head bool) (fileAnswer, error) {
	size, err := c.FileSize(p)
	if err != nil {
		c.Quit() //nolint:errcheck
		return fileAnswer{}, ftpError(err)
	}
	mod, _ := c.GetTime(p) // MDTM is optional: no time, no Last-Modified
	a, start, ok := rangeAnswer(size, mod, rangeHeader)
	if !ok || head {
		c.Quit() //nolint:errcheck
		return a, nil
	}
	r, err := c.RetrFrom(p, uint64(start))
	if err != nil {
		c.Quit() //nolint:errcheck
		return fileAnswer{}, ftpError(err)
	}
	a.body = &ftpBody{r: io.LimitReader(r, a.length), resp: r, c: c}
	return a, nil
}

func ftpList(c *ftp.ServerConn, p string, head bool) (fileAnswer, error) {
	dir := strings.TrimSuffix(p, "/")
	if dir == "" {
		dir = "."
	}
	names, err := c.NameList(dir)
	if err != nil {
		return fileAnswer{}, ftpError(err)
	}
	return listAnswer(names, head), nil
}

func ftpStore(c *ftp.ServerConn, p string, body []byte) (fileAnswer, error) {
	if err := c.Stor(p, bytes.NewReader(body)); err != nil {
		return fileAnswer{}, ftpError(err)
	}
	return emptyAnswer(), nil
}

// ftpFileUnavailable is the FTP reply "file unavailable" (RFC 959).
const ftpFileUnavailable = 550

// ftpError marks a 550 reply as ErrRemoteNotFound.
func ftpError(err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) && reply.Code == ftpFileUnavailable {
		return fmt.Errorf("%w: %w", ErrRemoteNotFound, err)
	}
	return err
}

// ftpBody is a RETR data stream. Close ends the transfer and the session.
type ftpBody struct {
	r    io.Reader
	resp *ftp.Response
	c    *ftp.ServerConn
}

func (b *ftpBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *ftpBody) Close() error {
	b.resp.SetDeadline(time.Now()) //nolint:errcheck // a partial read must not wait for the rest
	err := b.resp.Close()
	b.c.Quit() //nolint:errcheck
	return err
}
