package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// deadURL returns a URL on a port where nothing listens.
func deadURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr + "/"
}

// TestExecute_CurlExitCodes checks that each failure cause exits with curl's
// code for it (audit finding M6, plan phase 1 step 9). A script that already
// reads curl's codes can tell a DNS failure from a refused connection, a
// timeout, a bad certificate or an HTTP error without parsing stderr.
func TestExecute_CurlExitCodes(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hang.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer tlsSrv.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/missing":
			http.NotFound(w, r)
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer plain.Close()
	// garbage answers a TLS hello with bytes that are not TLS and not HTTP,
	// and keeps the connection open until the client closes it.
	garbage, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer garbage.Close()
	go func() {
		for {
			c, err := garbage.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("garbage, not TLS\r\n")) //nolint:errcheck
				io.Copy(io.Discard, c)                  //nolint:errcheck
			}()
		}
	}()
	garbageURL := "https://" + garbage.Addr().String() + "/"
	// certSrv refuses a client without a certificate. In TLS 1.3 the alert
	// comes after the client's handshake is done, so curl 8.18.0 exits 56
	// (400 of 400 runs: "Recv failure: Connection was aborted").
	certSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certSrv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	certSrv.Config.ErrorLog = log.New(io.Discard, "", 0)
	certSrv.StartTLS()
	defer certSrv.Close()
	httpsToPlain := "https://" + plain.Listener.Addr().String() + "/"
	noDir := filepath.Join(t.TempDir(), "no-such-dir", "f")

	tests := []struct {
		name string
		args []string
		want config.ExitCode
	}{
		{"unknown flag", []string{"--no-such-flag", plain.URL}, 2},
		{"unknown host", []string{"http://no-such-host.invalid/"}, 6},
		{"refused", []string{deadURL(t)}, 7},
		{"refused, -o", []string{"-o", filepath.Join(t.TempDir(), "f"), deadURL(t)}, 7},
		{"-f on 404", []string{plain.URL + "/missing"}, 22},
		{"-f on 404, -o", []string{"-o", filepath.Join(t.TempDir(), "f"), plain.URL + "/missing"}, 22},
		{"cannot write the file", []string{"-o", noDir, plain.URL}, 23},
		{"read file missing", []string{"-d", "@" + filepath.Join(t.TempDir(), "missing"), plain.URL}, 26},
		{"--timeout", []string{"--timeout", "0.3", hang.URL}, 28},
		{"--timeout, -o", []string{"--timeout", "0.3", "-o", filepath.Join(t.TempDir(), "f"), hang.URL}, 28},
		{"--stall-rate", []string{"--stall-rate", "1000", "--stall-time", "1", hang.URL}, 28},
		{"TLS to a plain server", []string{httpsToPlain}, 35},
		{"TLS to a plain server, impersonate", []string{"--impersonate", "chrome", httpsToPlain}, 35},
		{"not TLS", []string{garbageURL}, 35},
		{"not TLS, impersonate", []string{"--impersonate", "chrome", garbageURL}, 35},
		{"TLS alert after the handshake", []string{"-k", certSrv.URL}, 56},
		{"TLS alert after the handshake, impersonate", []string{"-k", "--impersonate", "chrome", certSrv.URL}, 56},
		{"redirect limit", []string{"--follow-max", "2", plain.URL + "/loop"}, 47},
		{"redirect limit, -o", []string{"--follow-max", "2", "-o", filepath.Join(t.TempDir(), "f"), plain.URL + "/loop"}, 47},
		{"untrusted certificate", []string{tlsSrv.URL}, 60},
		{"untrusted certificate, -o", []string{"-o", filepath.Join(t.TempDir(), "f"), tlsSrv.URL}, 60},
		{"untrusted certificate, impersonate", []string{"--impersonate", "chrome", tlsSrv.URL}, 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			if got := execute(ctx, append([]string{"-q", "-r", "0"}, tc.args...), nil, nil, out); got != tc.want.Int() {
				t.Errorf("ghola %q exit = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// TestExitFor_WrappedCauses checks causes that are hard to cause from a test
// server. Each one is wrapped, as the client wraps it, so the check must
// look through the chain.
func TestExitFor_WrappedCauses(t *testing.T) {
	tests := []struct {
		err  error
		want config.ExitCode
	}{
		{fmt.Errorf("download: %w", gholatransport.ErrConnectTimeout), 28},
		{fmt.Errorf("download: %w", gholatransport.ErrTooSlow), 28},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), 28},
		{fmt.Errorf("download: %w", client.ErrTooManyRedirects), 47},
		{fmt.Errorf("x: %w", config.ErrHTTPStatus), 22},
		{config.WriteError(errors.New("disk full")), 23},
		// A TLS alert during the handshake (before GotConn).
		{fmt.Errorf("x: %w", &net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}), 35},
		{fmt.Errorf("x: %w: %w", gholatransport.ErrRecv, errors.New("connection reset")), 56},
		{errors.New("something else"), config.SendFailed},
	}
	for _, tc := range tests {
		if got := exitFor(tc.err); got != tc.want {
			t.Errorf("exitFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
