package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strings"

	utls "github.com/bogdanfinn/utls"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// exitFor returns curl's exit code for the cause of err (audit finding M6).
// The order matters: a timeout during DNS is a timeout, and a TLS failure
// is not a refused connection.
func exitFor(err error) config.ExitCode {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, config.ErrHTTPStatus):
		return config.HTTPError
	case errors.Is(err, config.ErrWrite):
		return config.WriteFileFailed
	case errors.Is(err, client.ErrTooManyRedirects):
		return config.TooManyRedirects
	case errors.Is(err, client.ErrRedirectScheme):
		return config.UnsupportedProtocol
	case errors.Is(err, gholatransport.ErrFileRead):
		return config.FileCouldntRead
	case errors.Is(err, gholatransport.ErrLoginDenied):
		return config.FTPLoginDenied
	case errors.Is(err, gholatransport.ErrRemoteNotFound):
		return config.RemoteFileNotFound
	case errors.Is(err, gholatransport.ErrNoHTTPProxy):
		return config.UnsupportedProtocol
	case errors.Is(err, gholatransport.ErrBadEncoding):
		return config.BadContentEncoding
	case isTimeout(err):
		return config.TimedOut
	case errors.Is(err, gholatransport.ErrRecv):
		return config.RecvFailed
	case errors.As(err, &dnsErr):
		return config.CouldntResolveHost
	case errors.Is(err, gholatransport.ErrPinMismatch):
		return config.PinnedKeyMismatch
	case isCertError(err), errors.Is(err, gholatransport.ErrSSHHostKey):
		return config.PeerFailedVerification
	case isTLSError(err):
		return config.TLSConnectFailed
	case isDialError(err):
		return config.CouldntConnect
	default:
		return config.SendFailed
	}
}

// isTimeout reports a limit that ended the transfer: --max-time (the context
// deadline), --connect-timeout, --speed-limit, or a network timeout.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, gholatransport.ErrConnectTimeout) ||
		errors.Is(err, gholatransport.ErrTooSlow) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

// isCertError reports a server certificate that did not verify.
func isCertError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var authErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	return errors.As(err, &verifyErr) || errors.As(err, &authErr) ||
		errors.As(err, &hostErr) || errors.As(err, &invalidErr)
}

// isTLSError reports a TLS handshake that failed for a cause other than the
// certificate:
//   - A record that is not TLS. --impersonate uses utls, which has its own
//     RecordHeaderError type.
//   - A TLS alert from the server. crypto/tls and utls report it as a
//     net.OpError with Op "remote error".
//   - A plain-HTTP answer. net/http returns http.ErrSchemeMismatch, and fhttp
//     builds the same text with errors.New, so only the text identifies it.
func isTLSError(err error) bool {
	var recordErr tls.RecordHeaderError
	var uRecordErr utls.RecordHeaderError
	var opErr *net.OpError
	return errors.As(err, &recordErr) || errors.As(err, &uRecordErr) ||
		(errors.As(err, &opErr) && opErr.Op == "remote error") ||
		errors.Is(err, http.ErrSchemeMismatch) ||
		strings.Contains(err.Error(), http.ErrSchemeMismatch.Error())
}

// isDialError reports a TCP connection that failed.
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}
