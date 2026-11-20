package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// sshServer is an in-process SSH server for tests with the sftp subsystem
// and "scp -f" / "scp -t" exec requests, rooted at a temporary folder. It
// accepts one password, and, when set, one public key.
type sshServer struct {
	ln       net.Listener
	root     string
	hostKey  ssh.Signer
	password string
	userKey  ssh.PublicKey
	mu       sync.Mutex
	logins   []string // "password" or "publickey" for each accepted login
}

func newSSHServer(t *testing.T, password string, userKey ssh.PublicKey) *sshServer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshServer{ln: ln, root: t.TempDir(), hostKey: hostKey, password: password, userKey: userKey}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if s.password != "" && string(pass) == s.password {
				s.login("password")
				return nil, nil
			}
			return nil, fmt.Errorf("bad password")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if s.userKey != nil && string(key.Marshal()) == string(s.userKey.Marshal()) {
				s.login("publickey")
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		},
	}
	cfg.AddHostKey(hostKey)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, cfg)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *sshServer) login(how string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins = append(s.logins, how)
}

func (s *sshServer) addr() string { return s.ln.Addr().String() }

// knownHostsLine is the known_hosts line for this server.
func (s *sshServer) knownHostsLine() string {
	host, port, _ := net.SplitHostPort(s.addr())
	return fmt.Sprintf("[%s]:%s %s", host, port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey.PublicKey()))))
}

func (s *sshServer) write(name string, b []byte) {
	p := filepath.Join(s.root, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0o755) //nolint:errcheck
	os.WriteFile(p, b, 0o644)           //nolint:errcheck
}

func (s *sshServer) read(name string) []byte {
	b, _ := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(name)))
	return b
}

func (s *sshServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions") //nolint:errcheck
			continue
		}
		ch, reqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, reqs)
	}
}

func (s *sshServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			if string(req.Payload[4:]) != "sftp" {
				req.Reply(false, nil) //nolint:errcheck
				continue
			}
			req.Reply(true, nil) //nolint:errcheck
			srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.root))
			if err == nil {
				srv.Serve() //nolint:errcheck
			}
			return
		case "exec":
			req.Reply(true, nil) //nolint:errcheck
			s.scp(ch, string(req.Payload[4:]))
			return
		default:
			req.Reply(false, nil) //nolint:errcheck
		}
	}
}

// scp serves "scp -f PATH" (send a file) and "scp -t PATH" (receive one),
// the source and sink sides of the scp protocol.
func (s *sshServer) scp(ch ssh.Channel, cmd string) {
	status := func(code uint32) {
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{code})) //nolint:errcheck
	}
	f := strings.Fields(cmd)
	if len(f) < 3 || f[0] != "scp" {
		status(1)
		return
	}
	p := filepath.Join(s.root, filepath.FromSlash(strings.Trim(f[len(f)-1], "'")))
	buf := make([]byte, 1)
	switch f[1] {
	case "-f":
		ch.Read(buf) //nolint:errcheck // the client's first 0 byte
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(ch, "\x01scp: %s: No such file or directory\n", p)
			status(1)
			return
		}
		fmt.Fprintf(ch, "C0644 %d %s\n", len(b), filepath.Base(p))
		ch.Read(buf)        //nolint:errcheck
		ch.Write(b)         //nolint:errcheck
		ch.Write([]byte{0}) //nolint:errcheck
		ch.Read(buf)        //nolint:errcheck
		status(0)
	case "-t":
		ch.Write([]byte{0}) //nolint:errcheck
		var mode string
		var size int64
		var name string
		fmt.Fscanf(ch, "%s %d %s\n", &mode, &size, &name) //nolint:errcheck
		ch.Write([]byte{0})                               //nolint:errcheck
		b := make([]byte, size)
		io.ReadFull(ch, b)        //nolint:errcheck
		ch.Read(buf)              //nolint:errcheck
		os.WriteFile(p, b, 0o644) //nolint:errcheck
		ch.Write([]byte{0})       //nolint:errcheck
		status(0)
	}
}
