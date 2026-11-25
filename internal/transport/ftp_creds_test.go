package transport

import (
	"testing"

	"github.com/robot-accomplice/ghola/internal/config"
)

// -u belongs to the server that the user named. An FTP server that ghola
// reaches by a redirect from another host must not get it: ghola logs in
// as anonymous there. Before, ftpLogin sent -u to any FTP host.
func TestFTP_NoCredentialsForAnotherHost(t *testing.T) {
	srv := newFTPServer(t, "", ftpFiles())
	opts := &config.Options{URL: "http://first.test/start", User: "zed:leak123"}
	if _, err := ftpDo(t, opts, "", "GET", "ftp://"+srv.addr()+"/pub/a.txt", "", nil); err != nil {
		t.Fatal(err)
	}
	if srv.saw("PASS leak123") || srv.saw("USER zed") {
		t.Error("the FTP server on another host got -u")
	}
	trusted := &config.Options{URL: "http://first.test/start", User: "zed:ok", Resilience: config.ResilienceOptions{LocationTrusted: true}}
	if _, err := ftpDo(t, trusted, "", "GET", "ftp://"+srv.addr()+"/pub/a.txt", "", nil); err != nil {
		t.Fatal(err)
	}
	if !srv.saw("USER zed") {
		t.Error("--follow-trusted: the FTP server did not get -u")
	}
}
