package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --rate-max-total limits all transfers together, as aria2c
// --max-overall-download-limit does; --rate-max limits each one. Four
// parallel 20 KB downloads under a 40 KB/s total take about 2 s, and
// under 40 KB/s each about 0.5 s.
func TestExecute_RateMaxTotal(t *testing.T) {
	body := strings.Repeat("x", 20<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer srv.Close()
	urls := []string{srv.URL + "/a", srv.URL + "/b", srv.URL + "/c", srv.URL + "/d"}
	run := func(limit ...string) time.Duration {
		args := append([]string{"download", "-q", "-j", "4", "-o", t.TempDir() + "/"}, limit...)
		start := time.Now()
		if code := runGhola(t, append(args, urls...)...); code != 0 {
			t.Fatalf("%v: exit %d", limit, code)
		}
		return time.Since(start)
	}
	if d := run("--rate-max-total", "40K"); d < 1500*time.Millisecond {
		t.Errorf("--rate-max-total 40K: 80 KB in %v; the limit did not hold", d)
	}
	if d := run("--rate-max", "40K"); d > 1200*time.Millisecond {
		t.Errorf("--rate-max 40K for each: %v; the transfers did not run side by side", d)
	}
}
