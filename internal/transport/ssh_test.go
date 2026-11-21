package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
	"golang.org/x/crypto/ssh"
)

// useSSHDir points ghola's ~/.ssh at a temporary folder with these
// known_hosts lines, and returns the folder.
func useSSHDir(t *testing.T, knownHosts ...string) string {
	t.Helper()
	dir := t.TempDir()
	if len(knownHosts) > 0 {
		var b []byte
		for _, l := range knownHosts {
			b = append(b, l+"\n"...)
		}
		os.WriteFile(filepath.Join(dir, "known_hosts"), b, 0o600) //nolint:errcheck
	}
	old := sshDir
	sshDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { sshDir = old })
	t.Setenv("SSH_AUTH_SOCK", "")
	return dir
}

func sshDo(t *testing.T, opts *config.Options, proxy, method, url, rng string, body []byte) (string, *fasthttp.Response, error) {
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
	if err := tr.Do(context.Background(), req, rsp); err != nil {
		return "", rsp, err
	}
	b, err := ReadBody(rsp)
	return string(b), rsp, err
}

// Both SSH protocols download a file with a password login, and a range
// gives 206: sftp seeks, scp reads and drops the first bytes.
func TestSSH_DownloadAndRange(t *testing.T) {
	srv := newSSHServer(t, "s3cret", nil)
	srv.write("pub/a.txt", []byte("0123456789"))
	useSSHDir(t, srv.knownHostsLine())
	for _, scheme := range []string{"sftp", "scp"} {
		t.Run(scheme, func(t *testing.T) {
			url := scheme + "://alice:s3cret@" + srv.addr() + "/~/pub/a.txt"
			if got, _, err := sshDo(t, &config.Options{}, "", "GET", url, "", nil); err != nil || got != "0123456789" {
				t.Fatalf("body %q, err %v", got, err)
			}
			got, rsp, err := sshDo(t, &config.Options{}, "", "GET", url, "bytes=6-", nil)
			if err != nil || got != "6789" || rsp.StatusCode() != 206 {
				t.Fatalf("range: status %d, body %q, err %v", rsp.StatusCode(), got, err)
			}
		})
	}
}

// Without a known_hosts line for the server, the connection stops before
// the login (exit 60): a host in the middle cannot take the password.
// -k skips the check, as in curl.
func TestSSH_HostKeyCheck(t *testing.T) {
	srv := newSSHServer(t, "s3cret", nil)
	srv.write("a.txt", []byte("x"))
	url := "sftp://alice:s3cret@" + srv.addr() + "/~/a.txt"

	useSSHDir(t) // no known_hosts file
	if _, _, err := sshDo(t, &config.Options{}, "", "GET", url, "", nil); !errors.Is(err, ErrSSHHostKey) {
		t.Fatalf("no known_hosts: err = %v, want ErrSSHHostKey", err)
	}
	other := newSSHServer(t, "", nil) // a different key for the same address
	useSSHDir(t, other.knownHostsLine()[len(other.addr())+3:]+"")
	host := "[" + srv.addr()[:len("127.0.0.1")] + "]:" + srv.addr()[len("127.0.0.1:"):]
	useSSHDir(t, host+" "+string(ssh.MarshalAuthorizedKey(other.hostKey.PublicKey()))[:len(ssh.MarshalAuthorizedKey(other.hostKey.PublicKey()))-1])
	if _, _, err := sshDo(t, &config.Options{}, "", "GET", url, "", nil); !errors.Is(err, ErrSSHHostKey) {
		t.Fatalf("changed key: err = %v, want ErrSSHHostKey", err)
	}
	srv.mu.Lock()
	logins := len(srv.logins)
	srv.mu.Unlock()
	if logins != 0 {
		t.Fatalf("ghola logged in to an untrusted host")
	}
	if got, _, err := sshDo(t, &config.Options{Stealth: config.StealthOptions{Insecure: true}}, "", "GET", url, "", nil); err != nil || got != "x" {
		t.Fatalf("-k: body %q, err %v", got, err)
	}
}

// A key in ~/.ssh logs in when no password is given, as with ssh.
func TestSSH_KeyLogin(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	srv := newSSHServer(t, "", sshPub)
	srv.write("a.txt", []byte("by key"))
	dir := useSSHDir(t, srv.knownHostsLine())
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "id_ed25519"), pem.EncodeToMemory(block), 0o600) //nolint:errcheck
	got, _, err := sshDo(t, &config.Options{}, "", "GET", "sftp://alice@"+srv.addr()+"/~/a.txt", "", nil)
	if err != nil || got != "by key" {
		t.Fatalf("body %q, err %v", got, err)
	}
}

// A wrong password is ErrLoginDenied (exit 67), and a missing file is
// ErrRemoteNotFound (exit 78), on both protocols.
func TestSSH_Errors(t *testing.T) {
	srv := newSSHServer(t, "s3cret", nil)
	useSSHDir(t, srv.knownHostsLine())
	for _, scheme := range []string{"sftp", "scp"} {
		if _, _, err := sshDo(t, &config.Options{}, "", "GET", scheme+"://alice:wrong@"+srv.addr()+"/~/a", "", nil); !errors.Is(err, ErrLoginDenied) {
			t.Errorf("%s wrong password: err = %v", scheme, err)
		}
		if _, _, err := sshDo(t, &config.Options{User: "alice:s3cret"}, "", "GET", scheme+"://"+srv.addr()+"/~/none", "", nil); !errors.Is(err, ErrRemoteNotFound) {
			t.Errorf("%s missing file: err = %v", scheme, err)
		}
	}
}

// -T uploads on both protocols, and an sftp path that ends in a folder
// lists it.
func TestSSH_UploadAndList(t *testing.T) {
	srv := newSSHServer(t, "s3cret", nil)
	srv.write("dir/b", nil)
	useSSHDir(t, srv.knownHostsLine())
	opts := &config.Options{User: "alice:s3cret"}
	for _, scheme := range []string{"sftp", "scp"} {
		if _, _, err := sshDo(t, opts, "", "PUT", scheme+"://"+srv.addr()+"/~/up-"+scheme, "", []byte("up "+scheme)); err != nil {
			t.Fatalf("%s upload: %v", scheme, err)
		}
		if got := string(srv.read("up-" + scheme)); got != "up "+scheme {
			t.Errorf("%s stored %q", scheme, got)
		}
	}
	if got, _, err := sshDo(t, opts, "", "GET", "sftp://"+srv.addr()+"/~/dir", "", nil); err != nil || got != "b\n" {
		t.Errorf("list: %q, err %v", got, err)
	}
}

// A SOCKS proxy carries SSH, and an HTTP proxy is refused.
func TestSSH_Proxies(t *testing.T) {
	srv := newSSHServer(t, "s3cret", nil)
	srv.write("a.txt", []byte("via socks"))
	useSSHDir(t, srv.knownHostsLine())
	socks := newSocksServer(t, "")
	url := "sftp://alice:s3cret@" + srv.addr() + "/~/a.txt"
	if got, _, err := sshDo(t, &config.Options{}, "socks5://"+socks.ln.Addr().String(), "GET", url, "", nil); err != nil || got != "via socks" {
		t.Fatalf("socks: body %q, err %v", got, err)
	}
	if v, _, _ := socks.last(); v != 5 {
		t.Error("the request did not go through the SOCKS proxy")
	}
	if _, _, err := sshDo(t, &config.Options{}, "http://127.0.0.1:1", "GET", url, "", nil); !errors.Is(err, ErrNoHTTPProxy) {
		t.Fatalf("http proxy: err = %v, want ErrNoHTTPProxy", err)
	}
}
