package transport

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ftpServer is a minimal FTP server for tests: USER, PASS, TYPE, FEAT,
// SIZE, MDTM, EPSV, PASV, REST, RETR, STOR, NLST, LIST and QUIT, with one
// user and an in-memory file tree. It records the commands it got.
type ftpServer struct {
	ln       net.Listener
	tls      *tls.Config // implicit FTPS on control and data when set
	user     string      // "user:pass"; "" accepts anyone
	mu       sync.Mutex
	files    map[string][]byte
	commands []string
}

func newFTPServer(t *testing.T, user string, files map[string][]byte) *ftpServer {
	return newFTPServerTLS(t, user, files, nil)
}

func newFTPServerTLS(t *testing.T, user string, files map[string][]byte, cfg *tls.Config) *ftpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		ln = tls.NewListener(ln, cfg)
	}
	s := &ftpServer{ln: ln, tls: cfg, user: user, files: files}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *ftpServer) addr() string { return s.ln.Addr().String() }

// dataListener opens a passive data listener, with TLS for FTPS.
func (s *ftpServer) dataListener() net.Listener {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	if s.tls != nil {
		return tls.NewListener(ln, s.tls)
	}
	return ln
}

func (s *ftpServer) saw(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (s *ftpServer) file(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[name]
	return b, ok
}

type ftpSession struct {
	s      *ftpServer
	w      io.Writer
	user   string
	authed bool
	rest   int64
	data   net.Listener
}

func (s *ftpServer) serve(c net.Conn) {
	defer c.Close()
	fs := &ftpSession{s: s, w: c}
	fs.reply(220, "test server")
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()
		cmd, arg, _ := strings.Cut(line, " ")
		if !fs.handle(strings.ToUpper(cmd), arg) {
			return
		}
	}
}

func (fs *ftpSession) reply(code int, msg string) { fmt.Fprintf(fs.w, "%d %s\r\n", code, msg) }

func (fs *ftpSession) handle(cmd, arg string) bool {
	switch cmd {
	case "USER":
		fs.user = arg
		fs.reply(331, "password")
	case "PASS":
		if fs.s.user != "" && fs.user+":"+arg != fs.s.user {
			fs.reply(530, "login incorrect")
			return true
		}
		fs.authed = true
		fs.reply(230, "logged in")
	case "QUIT":
		fs.reply(221, "bye")
		return false
	case "FEAT":
		fmt.Fprint(fs.w, "211-Features\r\n SIZE\r\n MDTM\r\n REST STREAM\r\n EPSV\r\n211 End\r\n")
	case "TYPE", "OPTS", "MODE", "PBSZ", "PROT":
		fs.reply(200, "ok")
	default:
		if !fs.authed {
			fs.reply(530, "not logged in")
			return true
		}
		fs.transfer(cmd, arg)
	}
	return true
}

func (fs *ftpSession) transfer(cmd, arg string) {
	switch cmd {
	case "SIZE":
		if b, ok := fs.s.file(arg); ok {
			fs.reply(213, strconv.Itoa(len(b)))
			return
		}
		fs.reply(550, "no such file")
	case "MDTM":
		fs.reply(213, "20231114221320")
	case "EPSV":
		ln := fs.s.dataListener()
		fs.data = ln
		fs.reply(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", ln.Addr().(*net.TCPAddr).Port))
	case "PASV":
		ln := fs.s.dataListener()
		fs.data = ln
		p := ln.Addr().(*net.TCPAddr).Port
		fs.reply(227, fmt.Sprintf("Entering Passive Mode (127,0,0,1,%d,%d)", p/256, p%256))
	case "REST":
		fs.rest, _ = strconv.ParseInt(arg, 10, 64)
		fs.reply(350, "restarting")
	case "RETR":
		b, ok := fs.s.file(arg)
		if !ok {
			fs.reply(550, "no such file")
			return
		}
		fs.send(b[min(fs.rest, int64(len(b))):])
		fs.rest = 0
	case "NLST", "LIST":
		dir := strings.TrimSuffix(arg, "/") + "/"
		var names []string
		fs.s.mu.Lock()
		for n := range fs.s.files {
			if strings.HasPrefix(n, dir) {
				names = append(names, strings.TrimPrefix(n, dir))
			}
		}
		fs.s.mu.Unlock()
		sort.Strings(names)
		fs.send([]byte(strings.Join(names, "\r\n") + "\r\n"))
	case "STOR":
		fs.reply(150, "send")
		c, err := fs.data.Accept()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(c)
		c.Close()
		fs.s.mu.Lock()
		fs.s.files[arg] = b
		fs.s.mu.Unlock()
		fs.reply(226, "stored")
	default:
		fs.reply(502, "not implemented")
	}
}

func (fs *ftpSession) send(b []byte) {
	fs.reply(150, "opening")
	c, err := fs.data.Accept()
	if err != nil {
		return
	}
	c.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	c.Write(b)                                     //nolint:errcheck
	c.Close()
	fs.reply(226, "done")
}
