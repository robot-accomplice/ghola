package main

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// rawServer answers every connection with reply and closes it.
func rawServer(t *testing.T, reply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				c.SetReadDeadline(time.Now().Add(time.Second)) //nolint:errcheck
				c.Read(buf)                                    //nolint:errcheck // the request
				io.WriteString(c, reply)                       //nolint:errcheck
			}()
		}
	}()
	return "http://" + ln.Addr().String() + "/"
}

// A broken answer exits with curl's code for it, so a script can tell a
// cut body from a server that sent nothing. ghola exited 55 or 56 for all
// of them.
func TestExecute_BrokenReplyExitCodes(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  int
	}{
		{"body shorter than Content-Length", "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort", 18},
		{"empty reply", "", 52},
		{"not HTTP", "garbage garbage\r\n\r\n", 1},
		{"gzip encoding, body not gzip", "HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\nContent-Length: 9\r\n\r\nnot gzip!", 61},
		{"deflate encoding, body not deflate", "HTTP/1.1 200 OK\r\nContent-Encoding: deflate\r\nContent-Length: 9\r\n\r\nnot zlib!", 61},
		{"br encoding, body not br", "HTTP/1.1 200 OK\r\nContent-Encoding: br\r\nContent-Length: 9\r\n\r\nnot brot!", 61},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := rawServer(t, tc.reply)
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if got := execute(ctx, []string{"-q", "-r", "0", u}, nil, nil, out); got != tc.want {
				t.Errorf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}
