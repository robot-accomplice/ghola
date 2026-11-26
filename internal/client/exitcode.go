package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	utls "github.com/bogdanfinn/utls"

	"github.com/robot-accomplice/ghola/internal/config"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
)

// curl's exit codes that config does not name.
const (
	// PartialFile is a body that ended before its Content-Length (curl 18).
	PartialFile config.ExitCode = 18
	// GotNothing is a server that closed the connection without an answer
	// (curl 52).
	GotNothing config.ExitCode = 52
)

// ErrInvalidRequest marks a request value that cannot go on the wire: a
// method or a header name that is not a token, a control byte in a
// header value, or a URL with a space or a line break. A retry cannot
// cure it, so it exits 2 at once.
var ErrInvalidRequest = errors.New("invalid request value")

// ExitCodeFor returns curl's exit code for the cause of err (audit finding M6).
// The order matters: a timeout during DNS is a timeout, and a TLS failure
// is not a refused connection.
func ExitCodeFor(err error) config.ExitCode {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, config.ErrBadOption):
		return config.BadFlag
	case errors.Is(err, config.ErrHTTPStatus):
		return config.HTTPError
	case errors.Is(err, config.ErrChecksum):
		return config.ChecksumFailed
	case errors.Is(err, config.ErrTooLarge):
		return config.FileSizeExceeded
	case errors.Is(err, config.ErrWrite):
		return config.WriteFileFailed
	case errors.Is(err, ErrTooManyRedirects):
		return config.TooManyRedirects
	case errors.Is(err, ErrRedirectScheme):
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
	case errors.Is(err, io.ErrUnexpectedEOF):
		return PartialFile // the body ended before its length
	case errors.Is(err, gholatransport.ErrRecv) && errors.Is(err, io.EOF):
		return GotNothing // the server closed the connection without an answer
	case isMalformedReply(err):
		return config.UnsupportedProtocol // as curl: the answer is not HTTP
	case errors.Is(err, gholatransport.ErrRecv):
		return config.RecvFailed
	case errors.As(err, &dnsErr), errors.Is(err, gholatransport.ErrNoAddress):
		return config.CouldntResolveHost
	case errors.Is(err, gholatransport.ErrCertStatus):
		return config.InvalidCertStatus
	case errors.Is(err, gholatransport.ErrCertRevoked):
		return config.PeerFailedVerification
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

// isMalformedReply reports an answer that is not HTTP. net/http has no
// error type for it, so only the text identifies it.
func isMalformedReply(err error) bool {
	return strings.Contains(err.Error(), "malformed HTTP")
}

// permanent reports an error that a retry cannot cure: the same request
// gets the same answer, and a login that the server refused can lock the
// account when ghola sends it again. A refused or reset connection, a
// timeout and a receive failure can pass, so they are retried.
func permanent(err error) bool {
	switch ExitCodeFor(err) {
	case config.BadFlag, config.UnsupportedProtocol, config.URLMalformat,
		config.FileCouldntRead, config.ReadFileFailed,
		config.FTPLoginDenied, config.RemoteFileNotFound, config.TooManyRedirects,
		config.TLSConnectFailed, config.PeerFailedVerification, config.PinnedKeyMismatch,
		config.InvalidCertStatus, config.BadContentEncoding, config.FileSizeExceeded,
		config.WriteFileFailed, config.ChecksumFailed:
		return true
	}
	return false
}
