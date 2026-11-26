package transport

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// --interface and --local-port set the source of the connection, on both
// stacks: a firewall rule or a test can then expect it. The server sees
// the address and a port in the range.
func TestLocalDial_SourceAddress(t *testing.T) {
	var mu sync.Mutex
	var remote string
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		remote = r.RemoteAddr
		mu.Unlock()
	}))
	defer ts.Close()
	// Hold the first port of the range, so the dial must move on.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	first := held.Addr().(*net.TCPAddr).Port
	portRangeArg := strconv.Itoa(first) + "-" + strconv.Itoa(first+50)
	for _, imp := range stacks {
		t.Run("impersonate="+imp, func(t *testing.T) {
			opts := &config.Options{
				Stealth: config.StealthOptions{Impersonate: imp},
				Net:     config.NetOptions{Interface: "127.0.0.1", LocalPort: portRangeArg},
			}
			tr, err := New(opts, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := get(t, tr, ts.URL+"/"); err != nil {
				t.Fatalf("GET: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			host, p, _ := net.SplitHostPort(remote)
			port, _ := strconv.Atoi(p)
			if host != "127.0.0.1" || port <= first || port > first+50 {
				t.Errorf("source %s, want 127.0.0.1 with a port in %s after the held one", remote, portRangeArg)
			}
		})
	}
}

// --unix-socket sends HTTP through the socket, whatever the host of the
// URL, on both stacks.
func TestLocalDial_UnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.Listener = ln
	ts.Start()
	defer ts.Close()
	for _, imp := range stacks {
		tr, err := New(&config.Options{Stealth: config.StealthOptions{Impersonate: imp}, Net: config.NetOptions{UnixSocket: sock}}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := get(t, tr, "http://ghola-test.invalid/"); err != nil {
			t.Errorf("impersonate=%q: %v", imp, err)
		}
	}
}

// Bad values are errors.
func TestLocalDial_BadValues(t *testing.T) {
	for _, n := range []config.NetOptions{
		{LocalPort: "0"}, {LocalPort: "9-3"}, {LocalPort: "x"}, {LocalPort: "1-70000"},
		{Interface: "no-such-interface-ghola"},
	} {
		if _, _, err := localDial(&config.Options{Net: n}, true); err == nil {
			t.Errorf("%+v was accepted", n)
		}
	}
}
