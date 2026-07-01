package client

import (
	"bytes"
	"testing"
	"time"
)

func TestRateLimitedWriter_Throttles(t *testing.T) {
	rl := newRateLimiter(100_000) // 100 KB/s
	var sink bytes.Buffer
	w := newRateLimitedWriter(&sink, rl)
	data := make([]byte, 50_000) // 0.5s worth at 100 KB/s

	start := time.Now()
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 300*time.Millisecond {
		t.Errorf("write took %v, expected >= ~0.5s of throttling", elapsed)
	}
	if sink.Len() != len(data) {
		t.Errorf("wrote %d bytes, want %d", sink.Len(), len(data))
	}
}

func TestProgressWriter_CountsAll(t *testing.T) {
	var sink, prog bytes.Buffer
	pw := newProgressWriter(&sink, &prog, 10)
	if _, err := pw.Write(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	pw.done()
	if pw.written != 10 || sink.Len() != 10 {
		t.Fatalf("written=%d sink=%d, want 10/10", pw.written, sink.Len())
	}
}

// TestProgressTap_CountsIntoSharedProgress verifies that the per-segment tap
// correctly increments the shared progressWriter's byte counter without
// mutating its dst field.
func TestProgressTap_CountsIntoSharedProgress(t *testing.T) {
	var shared bytes.Buffer  // progress output
	var seg1, seg2 bytes.Buffer // per-segment sinks

	// shared progress with io.Discard as initial dst (segments use taps)
	pw := newProgressWriter(bytes.NewBuffer(nil), &shared, 20)

	tap1 := newProgressTap(&seg1, pw)
	tap2 := newProgressTap(&seg2, pw)

	if _, err := tap1.Write(make([]byte, 10)); err != nil {
		t.Fatalf("tap1 write: %v", err)
	}
	if _, err := tap2.Write(make([]byte, 10)); err != nil {
		t.Fatalf("tap2 write: %v", err)
	}
	if pw.written != 20 {
		t.Errorf("shared written=%d, want 20", pw.written)
	}
	if seg1.Len() != 10 || seg2.Len() != 10 {
		t.Errorf("seg1=%d seg2=%d, want 10 each", seg1.Len(), seg2.Len())
	}
}
