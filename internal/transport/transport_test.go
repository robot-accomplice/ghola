package transport

import (
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

func TestNewSelectsSimpleTransportWithoutImpersonation(t *testing.T) {
	tr, err := New(&config.Options{}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tr.Name() != "fasthttp" {
		t.Fatalf("transport name=%q, want fasthttp", tr.Name())
	}
}

func TestNewSelectsTLSClientTransportWithImpersonation(t *testing.T) {
	tr, err := New(&config.Options{Impersonate: "chrome"}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if tr.Name() != "tls-client" {
		t.Fatalf("transport name=%q, want tls-client", tr.Name())
	}
}

func TestNewPreservesBufferSizeOnSimpleTransport(t *testing.T) {
	tr, err := New(&config.Options{BufferSize: 8192}, "")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	simple, ok := tr.(*simpleTransport)
	if !ok {
		t.Fatalf("transport type=%T, want *simpleTransport", tr)
	}
	if simple.client.ReadBufferSize != 8192 {
		t.Fatalf("read buffer size=%d, want 8192", simple.client.ReadBufferSize)
	}
}
