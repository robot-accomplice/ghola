package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/valyala/fasthttp"
)

// TestDownload_NoMeterWhenStderrIsNotATerminal checks that a download to a
// file prints no progress meter when stderr is a file (audit finding M4). A
// script that logs stderr must not get a log full of meter lines.
func TestDownload_NoMeterWhenStderrIsNotATerminal(t *testing.T) {
	data := bytes.Repeat([]byte("m"), 64*1024)
	streamer := blobServer(t, data)

	logFile, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	orig := os.Stderr
	os.Stderr = logFile
	defer func() { os.Stderr = orig }()

	opts := &config.Options{
		URL: "http://test/blob", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: filepath.Join(t.TempDir(), "blob")},
		Stealth: config.StealthOptions{Agent: "ghola-test"},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	os.Stderr = orig
	got, _ := os.ReadFile(logFile.Name())
	if len(got) != 0 {
		t.Errorf("stderr is a file, but got meter output %q", got)
	}
}

// TestDownload_MeterTotalIsDecodedLength checks that with --compressed the
// meter does not take the compressed Content-Length as the total (audit
// finding M4). The meter counts decoded bytes, so a compressed total gave
// values such as "5456.1%".
func TestDownload_MeterTotalIsDecodedLength(t *testing.T) {
	plain := bytes.Repeat([]byte("zip-me-"), 10000)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(plain) //nolint:errcheck
	zw.Close()      //nolint:errcheck
	ln, streamer := newStreamTestServer(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(200)
		ctx.Response.Header.Set("Content-Encoding", "gzip")
		if !ctx.IsHead() {
			ctx.SetBody(gz.Bytes())
		} else {
			ctx.Response.Header.SetContentLength(gz.Len())
		}
	})
	defer ln.Close()

	var meter bytes.Buffer
	origOut := meterOut
	meterOut = func() io.Writer { return &meter }
	defer func() { meterOut = origOut }()

	opts := &config.Options{
		URL: "http://test/z", Method: "GET", Concurrency: 1,
		Output:  config.OutputOptions{File: filepath.Join(t.TempDir(), "z")},
		Stealth: config.StealthOptions{Agent: "ghola-test", Compressed: true},
	}
	if err := Download(context.Background(), opts, streamer); err != nil {
		t.Fatalf("Download error: %v", err)
	}
	out := meter.String()
	if strings.Contains(out, "%") {
		t.Errorf("the decoded length is not known, but the meter shows a percentage: %q", out)
	}
	if !strings.Contains(out, itoa(len(plain))+" bytes") {
		t.Errorf("meter %q does not end at the %d decoded bytes", out, len(plain))
	}
}
