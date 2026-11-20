package transport

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The SSH schemes (plan phase 5, decision D1).
const (
	SchemeSFTP = "sftp"
	SchemeSCP  = "scp"
	sshPort    = "22"
)

// ErrSSHHostKey marks a server key that ~/.ssh/known_hosts does not hold,
// or that differs from it. Without this check, any host between ghola and
// the server could take the login. curl exits 60 for it, as for a TLS
// certificate. -k skips the check.
var ErrSSHHostKey = errors.New("the SSH host key is not trusted")

// sshDir is the folder of known_hosts and the user keys. Tests replace it.
var sshDir = func() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".ssh"), err
}

// userKeyFiles are the private keys that ghola offers, in OpenSSH's order.
// A key with a passphrase is skipped: ghola does not ask for one.
var userKeyFiles = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

// dialSSH connects, checks the host key and logs in.
func (t protocolTransport) dialSSH(ctx context.Context, u *url.URL) (*ssh.Client, error) {
	base, err := t.tunnelDialer(u)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = sshPort
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	hostKey, err := t.hostKeyCallback()
	if err != nil {
		return nil, err
	}
	name, auth := t.sshAuth(u)
	cfg := &ssh.ClientConfig{User: name, Auth: auth, HostKeyCallback: hostKey}
	conn, err := base(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d) //nolint:errcheck
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, sshError(err)
	}
	conn.SetDeadline(time.Time{}) //nolint:errcheck
	return ssh.NewClient(c, chans, reqs), nil
}

// hostKeyCallback checks the server key against ~/.ssh/known_hosts. With no
// known_hosts file, every host is unknown, so the check fails: a first
// connection needs -k or a known_hosts line (ssh-keyscan writes one).
func (t protocolTransport) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if t.opts.Stealth.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // -k asks for this, as in curl
	}
	dir, err := sshDir()
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "known_hosts")
	check, err := knownhosts.New(file)
	if errors.Is(err, os.ErrNotExist) {
		return func(host string, _ net.Addr, _ ssh.PublicKey) error {
			return fmt.Errorf("%w: %s is not in %s (there is no such file)", ErrSSHHostKey, host, file)
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return func(host string, remote net.Addr, key ssh.PublicKey) error {
		if err := check(host, remote, key); err != nil {
			return fmt.Errorf("%w: %w", ErrSSHHostKey, err)
		}
		return nil
	}, nil
}

// sshAuth returns the user and the login methods: the password of -u or the
// URL, then the keys in ~/.ssh, then the ssh-agent. -u wins over the URL,
// and with no user ghola logs in as the local user, as ssh does.
func (t protocolTransport) sshAuth(u *url.URL) (string, []ssh.AuthMethod) {
	name, pass, hasPass := "", "", false
	switch {
	case t.opts.User != "":
		name, pass, hasPass = strings.Cut(t.opts.User, ":")
	case u.User != nil:
		name = u.User.Username()
		pass, hasPass = u.User.Password()
	}
	if name == "" {
		if me, err := user.Current(); err == nil {
			name = path.Base(filepath.ToSlash(me.Username)) // DOMAIN\user on Windows
		}
	}
	var auth []ssh.AuthMethod
	if hasPass {
		auth = append(auth, ssh.Password(pass), ssh.KeyboardInteractive(
			func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pass
				}
				return answers, nil
			}))
	}
	if signers := userKeys(); len(signers) > 0 {
		auth = append(auth, ssh.PublicKeys(signers...))
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := net.Dial("unix", sock); err == nil {
			auth = append(auth, ssh.PublicKeysCallback(agent.NewClient(c).Signers))
		}
	}
	return name, auth
}

func userKeys() []ssh.Signer {
	dir, err := sshDir()
	if err != nil {
		return nil
	}
	var signers []ssh.Signer
	for _, f := range userKeyFiles {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		if s, err := ssh.ParsePrivateKey(b); err == nil {
			signers = append(signers, s)
		}
	}
	return signers
}

// sshError marks a refused login (curl 67) and keeps a host key error.
func sshError(err error) error {
	if errors.Is(err, ErrSSHHostKey) {
		return err
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		return fmt.Errorf("%w: %w", ErrLoginDenied, err)
	}
	return err
}

// remotePath is the path of an sftp:// or scp:// URL. /~/ starts at the
// user's home folder, as in curl.
func remotePath(u *url.URL) string {
	if rest, ok := strings.CutPrefix(u.Path, "/~/"); ok {
		return rest
	}
	return u.Path
}

// serveSFTP answers an sftp:// request: a file (Stat gives the headers, a
// range seeks), a folder list, and PUT (-T).
func (t protocolTransport) serveSFTP(ctx context.Context, req *fasthttp.Request) (fileAnswer, error) {
	u, err := requestURL(req)
	if err != nil {
		return fileAnswer{}, err
	}
	sc, err := t.dialSSH(ctx, u)
	if err != nil {
		return fileAnswer{}, err
	}
	c, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return fileAnswer{}, err
	}
	closeAll := func() { c.Close(); sc.Close() }
	p := remotePath(u)
	method := string(req.Header.Method())
	if method == fasthttp.MethodPut {
		defer closeAll()
		f, err := c.Create(p)
		if err != nil {
			return fileAnswer{}, remoteError(err)
		}
		if _, err := f.Write(req.Body()); err != nil {
			f.Close()
			return fileAnswer{}, err
		}
		return emptyAnswer(), f.Close()
	}
	if method != fasthttp.MethodGet && method != fasthttp.MethodHead {
		closeAll()
		return fileAnswer{}, fmt.Errorf("sftp://: the %s method is not supported", method)
	}
	info, err := c.Stat(p)
	if err != nil {
		closeAll()
		return fileAnswer{}, remoteError(err)
	}
	if info.IsDir() {
		defer closeAll()
		entries, err := c.ReadDir(p)
		if err != nil {
			return fileAnswer{}, remoteError(err)
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		return listAnswer(names, method == fasthttp.MethodHead), nil
	}
	a, start, ok := rangeAnswer(info.Size(), info.ModTime(), string(req.Header.Peek("Range")))
	if !ok || method == fasthttp.MethodHead {
		closeAll()
		return a, nil
	}
	f, err := c.Open(p)
	if err != nil {
		closeAll()
		return fileAnswer{}, remoteError(err)
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		f.Close()
		closeAll()
		return fileAnswer{}, err
	}
	a.body = closer{Reader: io.LimitReader(f, a.length), close: func() error { f.Close(); closeAll(); return nil }}
	return a, nil
}

// serveSCP answers an scp:// request with the scp protocol over an exec
// session: "scp -f" sends a file, "scp -t" receives one. scp cannot seek,
// so a range reads and drops the bytes before it.
func (t protocolTransport) serveSCP(ctx context.Context, req *fasthttp.Request) (fileAnswer, error) {
	u, err := requestURL(req)
	if err != nil {
		return fileAnswer{}, err
	}
	sc, err := t.dialSSH(ctx, u)
	if err != nil {
		return fileAnswer{}, err
	}
	s, err := sc.NewSession()
	if err != nil {
		sc.Close()
		return fileAnswer{}, err
	}
	closeAll := func() { s.Close(); sc.Close() }
	p := remotePath(u)
	switch method := string(req.Header.Method()); method {
	case fasthttp.MethodPut:
		defer closeAll()
		return emptyAnswer(), scpSend(s, p, req.Body())
	case fasthttp.MethodGet, fasthttp.MethodHead:
		r, size, err := scpReceive(s, p)
		if err != nil {
			closeAll()
			return fileAnswer{}, err
		}
		a, start, ok := rangeAnswer(size, time.Time{}, string(req.Header.Peek("Range")))
		if !ok || method == fasthttp.MethodHead {
			closeAll()
			return a, nil
		}
		if _, err := io.CopyN(io.Discard, r, start); err != nil {
			closeAll()
			return fileAnswer{}, err
		}
		a.body = closer{Reader: io.LimitReader(r, a.length), close: func() error { closeAll(); return nil }}
		return a, nil
	default:
		closeAll()
		return fileAnswer{}, fmt.Errorf("scp://: the %s method is not supported", method)
	}
}

// scpReceive starts "scp -f" and reads the file header. It returns the
// file bytes as a reader and the size.
func scpReceive(s *ssh.Session, p string) (io.Reader, int64, error) {
	in, err := s.StdinPipe()
	if err != nil {
		return nil, 0, err
	}
	out, err := s.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := s.Start("scp -f " + shellQuote(p)); err != nil {
		return nil, 0, err
	}
	r := bufio.NewReader(out)
	in.Write([]byte{0}) //nolint:errcheck
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, 0, err
	}
	if line[0] == 1 || line[0] == 2 { // an scp warning or error
		msg := strings.TrimSpace(line[1:])
		if strings.Contains(msg, "No such file") {
			return nil, 0, fmt.Errorf("%w: %s", ErrRemoteNotFound, msg)
		}
		return nil, 0, errors.New("scp: " + msg)
	}
	f := strings.Fields(line) // C0644 <size> <name>
	if len(f) < 3 || !strings.HasPrefix(f[0], "C") {
		return nil, 0, fmt.Errorf("scp: unexpected header %q", strings.TrimSpace(line))
	}
	size, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("scp: bad size in %q", strings.TrimSpace(line))
	}
	in.Write([]byte{0}) //nolint:errcheck
	return r, size, nil
}

// scpSend writes body to p with "scp -t".
func scpSend(s *ssh.Session, p string, body []byte) error {
	in, err := s.StdinPipe()
	if err != nil {
		return err
	}
	out, err := s.StdoutPipe()
	if err != nil {
		return err
	}
	if err := s.Start("scp -t " + shellQuote(p)); err != nil {
		return err
	}
	ack := func() error {
		b := make([]byte, 1)
		if _, err := io.ReadFull(out, b); err != nil {
			return err
		}
		if b[0] != 0 {
			msg, _ := bufio.NewReader(out).ReadString('\n')
			return errors.New("scp: " + strings.TrimSpace(msg))
		}
		return nil
	}
	if err := ack(); err != nil {
		return err
	}
	fmt.Fprintf(in, "C0644 %d %s\n", len(body), path.Base(p))
	if err := ack(); err != nil {
		return err
	}
	in.Write(body)      //nolint:errcheck
	in.Write([]byte{0}) //nolint:errcheck
	if err := ack(); err != nil {
		return err
	}
	in.Close()
	return s.Wait()
}

// shellQuote quotes p for the remote shell that runs the scp command.
func shellQuote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// remoteError marks a missing remote file (curl 78).
func remoteError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrRemoteNotFound, err)
	}
	return err
}

// rangeAnswer is the HTTP-like answer for a file of size bytes and a Range
// header: 200, 206, or 416 (ok false). start is the first byte to send.
func rangeAnswer(size int64, mod time.Time, rangeHeader string) (a fileAnswer, start int64, ok bool) {
	h := http.Header{}
	h.Set("Accept-Ranges", "bytes")
	if !mod.IsZero() {
		h.Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
	}
	status, length := http.StatusOK, size
	if rangeHeader != "" {
		var valid bool
		if start, length, valid = parseRange(rangeHeader, size); !valid {
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
			return fileAnswer{status: http.StatusRequestedRangeNotSatisfiable, header: h, body: io.NopCloser(bytes.NewReader(nil))}, 0, false
		}
		status = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size))
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	return fileAnswer{status: status, header: h, body: io.NopCloser(bytes.NewReader(nil)), length: length}, start, true
}

func listAnswer(names []string, head bool) fileAnswer {
	var b bytes.Buffer
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	h := http.Header{}
	h.Set("Content-Length", strconv.Itoa(b.Len()))
	if head {
		b.Reset()
	}
	return fileAnswer{status: http.StatusOK, header: h, body: io.NopCloser(&b), length: int64(b.Len())}
}

func emptyAnswer() fileAnswer {
	return fileAnswer{status: http.StatusOK, header: http.Header{}, body: io.NopCloser(bytes.NewReader(nil))}
}

// closer is a reader with its own Close.
type closer struct {
	io.Reader
	close func() error
}

func (c closer) Close() error { return c.close() }
