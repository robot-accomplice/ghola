package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A connect timeout is retried on stdout and in a download alike, so the
// two paths of one command do not behave differently (Jon, 2026-11-26).
func TestExecute_ConnectTimeoutIsRetriedOnEachPath(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() { time.Sleep(3 * time.Second); c.Close() }() // never answers the TLS hello
		}
	}()
	u := "https://" + ln.Addr().String() + "/f"
	for name, args := range map[string][]string{
		"stdout":   {u},
		"download": {"download", "-o", filepath.Join(t.TempDir(), "f"), u},
	} {
		conns.Store(0)
		out, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		code := execute(ctx, append(append([]string{}, args[:len(args)-1]...), "-q", "--retry-delay", "10ms", "--timeout", "5", "--timeout-connect", "0.3", args[len(args)-1]), nil, nil, out)
		cancel()
		out.Close()
		if code != 28 || conns.Load() != 4 {
			t.Errorf("%s: exit %d after %d connections; want 28 after 4 (the 3 default retries)", name, code, conns.Load())
		}
	}
}
