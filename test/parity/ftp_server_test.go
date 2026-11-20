//go:build parity

package parity

import (
	"crypto/tls"
	"errors"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"
)

// ftpFiles are the files of the FTP server, in /pub. 1m is the same content
// as /file/1m on the HTTP server, so a check can use e.Ref("1m").
var ftpFiles = []string{"1m"}

// ftpDriver is the driver of the harness FTP server. It is a real FTP
// server (fclairamb/ftpserverlib), so the four tools talk to the same
// server code, not to a server that ghola's own tests describe.
type ftpDriver struct {
	fs       afero.Fs
	settings *ftpserver.Settings
}

func (d *ftpDriver) GetSettings() (*ftpserver.Settings, error) { return d.settings, nil }

func (d *ftpDriver) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "ghola parity FTP server", nil
}

func (d *ftpDriver) ClientDisconnected(ftpserver.ClientContext) {}

// AuthUser accepts every login, the anonymous one included, as a public
// FTP server does.
func (d *ftpDriver) AuthUser(ftpserver.ClientContext, string, string) (ftpserver.ClientDriver, error) {
	return d.fs, nil
}

func (d *ftpDriver) GetTLSConfig() (*tls.Config, error) {
	return nil, errors.New("the harness FTP server has no TLS")
}

// startFTP starts the FTP server with ftpFiles and returns its address.
func (s *server) startFTP() (string, error) {
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/pub", 0o755); err != nil {
		return "", err
	}
	for _, name := range ftpFiles {
		if err := afero.WriteFile(fs, "/pub/"+name, s.content(parseSize(name)), 0o644); err != nil {
			return "", err
		}
	}
	srv := ftpserver.NewFtpServer(&ftpDriver{fs: fs, settings: &ftpserver.Settings{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
	}})
	if err := srv.Listen(); err != nil {
		return "", err
	}
	go srv.Serve() //nolint:errcheck // ends when close stops the server
	s.ftp = srv
	return srv.Addr(), nil
}
