package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
)

// A bad option value is an argument error: exit 2, as in curl. A script
// must be able to tell "I called ghola wrong" from "the transfer failed"
// (55), and v1.0.0 sent these values to the network code, which exited 55.
func TestExecute_BadOptionValuesExit2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	zeroPin := "sha256//" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	for name, args := range map[string][]string{
		"--tls-pin not sha256":       {"--tls-pin", "md5//x"},
		"--tls-min above --tls-max":  {"--tls-min", "1.3", "--tls-max", "1.2"},
		"--tls-ciphers unknown":      {"--tls-ciphers", "NO-SUCH-SUITE"},
		"--tls-cert without key":     {"--tls-cert", "c.pem"},
		"--tls-cert-type P12":        {"--tls-cert", "c.p12", "--tls-key", "k", "--tls-cert-type", "P12"},
		"impersonate with --tls-min": {"--impersonate", "chrome", "--tls-min", "1.3"},
		"impersonate pin with h3":    {"--impersonate", "chrome", "--http", "3", "--tls-pin", zeroPin},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			args = append(append([]string{"-q", "-r", "0"}, args...), srv.URL)
			if got := execute(ctx, args, nil, nil, out); got != config.BadFlag.Int() {
				t.Errorf("ghola %q exit = %d, want %d", args, got, config.BadFlag)
			}
		})
	}
}
