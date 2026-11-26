// Package config handles CLI flag parsing, validation, and the Options type
// that carries all user-specified settings through the ghola pipeline.
package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/robot-accomplice/ghola/internal/localname"
	"github.com/robot-accomplice/ghola/internal/netrc"
	"github.com/robot-accomplice/ghola/internal/profile"
	"github.com/valyala/fasthttp"
)

// ExitCode represents a process exit status returned by ghola. The values
// are curl's exit codes for the same causes (audit finding M6), so a script
// written for curl reads them the same way.
type ExitCode int

const (
	// NoError indicates successful execution.
	NoError ExitCode = 0
	// UnsupportedProtocol indicates a protocol that ghola does not follow,
	// for example a redirect to file:// (curl 1).
	UnsupportedProtocol ExitCode = 1
	// URLMalformat indicates a bad URL glob (curl 3).
	URLMalformat ExitCode = 3
	// BadFlag indicates invalid or missing CLI arguments (curl 2).
	BadFlag ExitCode = 2
	// CouldntResolveHost indicates that DNS did not resolve the host (curl 6).
	CouldntResolveHost ExitCode = 6
	// CouldntConnect indicates that the TCP connection failed (curl 7).
	CouldntConnect ExitCode = 7
	// HTTPError indicates an HTTP status that -f refuses (curl 22).
	HTTPError ExitCode = 22
	// WriteFileFailed indicates output could not be written (curl 23).
	WriteFileFailed ExitCode = 23
	// ReadFileFailed indicates a local file could not be read, e.g.
	// --upload-file or -d @file (curl 26).
	ReadFileFailed ExitCode = 26
	// FileCouldntRead indicates a file:// URL whose file could not be read
	// (curl 37).
	FileCouldntRead ExitCode = 37
	// FTPLoginDenied indicates that the FTP server refused the login
	// (curl 67).
	FTPLoginDenied ExitCode = 67
	// RemoteFileNotFound indicates that the FTP file does not exist
	// (curl 78).
	RemoteFileNotFound ExitCode = 78
	// TimedOut indicates --max-time, --connect-timeout or --speed-limit
	// ended the transfer (curl 28).
	TimedOut ExitCode = 28
	// TLSConnectFailed indicates a TLS handshake failure (curl 35).
	TLSConnectFailed ExitCode = 35
	// TooManyRedirects indicates that -L reached --max-redirs (curl 47).
	TooManyRedirects ExitCode = 47
	// SendFailed indicates a request failure without a more exact code
	// (curl 55).
	SendFailed ExitCode = 55
	// RecvFailed indicates a failure after the connection was ready, for
	// example a reset or a TLS alert after the handshake (curl 56).
	RecvFailed ExitCode = 56
	// ChecksumFailed indicates a body that does not match --checksum or
	// Content-Digest. 32 is aria2's code for it; curl does not use 32.
	ChecksumFailed ExitCode = 32
	// FileSizeExceeded indicates a body larger than --max-size (curl 63).
	FileSizeExceeded ExitCode = 63
	// PeerFailedVerification indicates that the server certificate or its
	// host name did not verify (curl 60).
	PeerFailedVerification ExitCode = 60
	// PinnedKeyMismatch indicates that the server key does not match
	// --tls-pin (curl 90).
	PinnedKeyMismatch ExitCode = 90
	// InvalidCertStatus indicates a missing or bad OCSP staple under
	// --tls-ocsp (curl 91).
	InvalidCertStatus ExitCode = 91
	// BadContentEncoding indicates a Content-Encoding that --compressed
	// cannot decode (curl 61).
	BadContentEncoding ExitCode = 61
)

// Int returns the ExitCode as a plain int for use with os.Exit.
func (e ExitCode) Int() int {
	return int(e)
}

// OutputOptions controls how HTTP responses are rendered.
type OutputOptions struct {
	File    string
	Verbose bool
	Fail    bool
	Include bool
	// Template is the parsed --output-template. Info records the transfer
	// for it; main sets a new one for each URL.
	Template *Template
	Info     *TransferInfo
	// Events is the --output-events json log; nil writes nothing.
	Events *EventLog
	// InputArgs holds the native options of each --input URL, by the
	// index in URLs; nil for a URL of the command line.
	InputArgs [][]string
	// PerURL holds one -o for each URL, in order, when several URLs have
	// several -o. main sets each URL's target from it.
	PerURL []string
	// CondTime: the request has a --cond-time or --cond-etag-compare
	// condition. A 304 then writes no file, as curl -z does.
	CondTime bool
	// ETagSave is the --cond-etag-save path.
	ETagSave string
	// Trace is the --trace path ("-" is stderr); TraceOut is that file,
	// which ghola opens once for the command. TraceASCII is
	// --trace-format ascii, and TraceTime is --trace-time.
	Trace      string
	TraceOut   io.Writer
	TraceASCII bool
	TraceTime  bool
	// RateTotal is the --rate-max-total limiter, which every transfer of
	// the command shares. main makes it; nil without the option.
	RateTotal RateShare
	// RateTotalValue is the --rate-max-total value.
	RateTotalValue string
	// NoSession is --no-session: no state file, and no entry in the job
	// list. SessionArgs are the native options of the run, as
	// --name=value, which "ghola jobs resume" runs again.
	NoSession   bool
	SessionArgs []string
	// WARC is the --warc path; WARCOut is that file, which ghola opens
	// once for the command.
	WARC    string
	WARCOut io.Writer
	// MeterOut is where the meter of one job of --jobs goes: its line of
	// the board. nil is the default meter.
	MeterOut io.Writer
	// Background is --background; Log is --log, where the messages go.
	Background bool
	Log        string
	// OnComplete and OnError run after a download (--on-complete,
	// --on-error).
	OnComplete, OnError string
	// FileAllocation is --file-allocation: none, prealloc, falloc or trunc.
	FileAllocation string
	// Checksum is the --checksum digest that the body must match.
	Checksum *Digest
	// SaveHeaders is the --save-headers path ("-" is stdout). HeaderOut is
	// that file, which ghola opens once for the whole command.
	SaveHeaders  string
	HeaderOut    io.Writer
	Silent       bool
	Snoop        bool
	JQ           string
	HAR          string
	ContinueAt   string // wired in Task 5; resume forces single-stream when set
	Range        string // explicit byte range passthrough (e.g. "0-1023")
	RemoteName   bool   // -O: save as URL basename
	RemoteHeader bool   // -J: use Content-Disposition filename
	RemoteTime   bool   // -R: set mtime from Last-Modified
	LimitRate    string // --limit-rate: aggregate bytes/sec cap (e.g. "500k", "2M")
	// Stdout is -o -: write the body to stdout, also binary data to a
	// terminal, as in curl. File is then "".
	Stdout bool
	// Progress forces the progress meter: "on" shows it also when stderr is
	// not a terminal, "off" hides it, "" shows it only on a terminal.
	Progress string
	// HideErrors drops the error message on stderr. Only ghola curl -s
	// (without -S) sets it, as curl does; native -q keeps the errors.
	HideErrors bool
	// CreateDirs creates the missing folders of the output file.
	CreateDirs bool
	// NoOverwrite refuses to replace an existing output file (the default
	// of ghola download; --output-overwrite turns it off).
	NoOverwrite bool
	// MaxSize stops a body larger than this many bytes (--max-size).
	MaxSize int64
	// Dir is the folder of -o DIR/: the remote file name goes inside it.
	Dir string
	// FailFrom400 is the v1 rule for Fail: status 400 and above fails, as
	// in curl. The v0 rule fails every status that is not 2xx.
	FailFrom400 bool
}

// StealthOptions controls browser impersonation and identity features.
type StealthOptions struct {
	Impersonate    string
	StealthHeaders bool
	Drift          int
	Agent          string
	AgentExplicit  bool
	Referer        string
	AcceptLanguage string
	HTTP3          bool
	// HTTPVersion is the --http value: "" (auto), "1.1", "2", "2-prior",
	// "3" or "3-only".
	HTTPVersion string
	CookieJar   string
	Cookies     []string
	ProfileList bool
	Compressed  bool // wired in Task 9; true disables segmentation
	Insecure    bool
	CACert      string
	ClientCert  string
	ClientKey   string
	// TLSMin and TLSMax are "1.2" or "1.3" (--tls-min, --tls-max).
	TLSMin, TLSMax string
	// CADir is a folder of CA certificates (--tls-ca-dir).
	CADir string
	// Pins are sha256//BASE64 hashes of the server's public key
	// (--tls-pin), as curl --pinnedpubkey takes them.
	Pins []string
	// Ciphers are the --tls-ciphers names; CRL is the --tls-crl file;
	// OCSP requires a good staple (--tls-ocsp); CertType is the format of
	// --tls-cert (--tls-cert-type).
	Ciphers  []string
	CRL      string
	OCSP     bool
	CertType string
	// ECH sends Encrypted Client Hello when the host's HTTPS record offers
	// it (--tls-ech). It needs --doh.
	ECH bool
}

// ResilienceOptions controls retry, timeout, redirect, and buffer behavior.
type ResilienceOptions struct {
	// HTTPRedirectsOnly limits redirects to http and https. The bridge sets
	// it: it fetches http(s) for its clients and never another protocol.
	HTTPRedirectsOnly bool
	Timeout           int
	Retries           int
	Backoff           int
	// Wait is the pause between requests (--wait).
	Wait      time.Duration
	RetryHTTP bool
	Location  bool
	// RetryMaxTime stops retries once this time has passed since the
	// first attempt (--retry-max-time). 0 is no limit.
	RetryMaxTime time.Duration
	// ConnectTimeout limits the connection phase (--connect-timeout).
	ConnectTimeout time.Duration
	// SpeedLimit and SpeedTime stop a transfer that is slower than
	// SpeedLimit bytes per second for SpeedTime (--speed-limit, --speed-time).
	SpeedLimit int64
	SpeedTime  time.Duration
	// LocationTrusted sends -u, Authorization and Cookie to every server in
	// the redirect chain, as curl --location-trusted does.
	LocationTrusted bool
	MaxRedirs       int
	BufferSize      int
	// RetryStatus is the list of status codes that a retry may cure
	// (--retry-status). Empty means the v0 list.
	RetryStatus []int
}

// ProxyOptions controls proxy routing behavior.
type ProxyOptions struct {
	Proxy       string
	Auth        string
	File        string
	Strategy    string
	HealthCheck bool
	// TLS holds the --proxy-tls-* options, in the fields of the --tls-*
	// options, for an https proxy.
	TLS StealthOptions
	// Bypass is the --proxy-bypass list of hosts that go direct, in
	// no_proxy's format. nil reads no_proxy; it then applies only to a
	// proxy from the environment, as before.
	Bypass *string
}

// Options holds every user-configurable setting parsed from CLI flags.
type Options struct {
	URL           string
	URLs          []string // every URL on the command line, in order; URL is the first
	Method        string
	Data          string
	DataBinary    string
	DataURLEncode []string
	Form          []string
	Headers       []string
	Transfer      string
	// DataLiteral sends Data as it is: a value that starts with @ is not a
	// file name (--data-literal).
	DataLiteral bool
	User        string
	// SigV4 signs each request with User (--auth-aws-sigv4).
	SigV4 *SigV4
	// Auth is the --auth method for User: "basic" (or ""), "digest" or
	// "any".
	Auth        string
	Concurrency int
	// Jobs runs up to this many transfers at the same time (--jobs).
	Jobs int
	// Sources are other mirrors of the same file (--source). The
	// segments of a download go to all of them.
	Sources []string
	Chain   string
	// PathAsIs sends the URL path without removing ./ and ../
	// (--url-path-as-is).
	PathAsIs bool
	// HSTS is the --hsts cache file.
	HSTS string
	// AltSvc is the --alt-svc cache file.
	AltSvc string
	// RequestTarget replaces the path and query in the request line
	// (--request-target), such as * for OPTIONS.
	RequestTarget string

	Output  OutputOptions
	Mirror  MirrorOptions
	Stealth StealthOptions
	// CredentialURL is the URL whose server the credentials belong to: the
	// first URL of the run. SendsCredentials compares a target with it.
	CredentialURL string
	// Netrc are the entries that --auth-netrc read; UseNetrc picks the one
	// for each URL. UserFromFlag is true when -u gave User.
	Netrc        []netrc.Entry
	UserFromFlag bool
	Resilience   ResilienceOptions
	ProxyCfg     ProxyOptions
	Net          NetOptions
}

// NetOptions change where a TCP connection goes (--ip, --resolve,
// --connect-to). The host name of the URL stays for TLS and Host.
type NetOptions struct {
	IPVersion string   // "4", "6", or "" for both
	Resolve   []string // host:port:address
	ConnectTo []string // host:port:host2:port2
	// Interface is the local address or interface name (--interface).
	Interface string
	// LocalPort is the local port, or a range N-M (--local-port).
	LocalPort string
	// UnixSocket connects HTTP through this socket (--unix-socket).
	UnixSocket string
	// DoH resolves names with DNS over HTTPS at this URL (--doh).
	DoH string
}

// RateShare is a speed limit that several transfers share
// (--rate-max-total). Wait blocks until n bytes may pass.
type RateShare interface {
	Wait(n int)
}

// Version is set at build time via -ldflags.
var Version = "dev"

// UsageError is an argument error. It carries the usage text so the CLI can
// show the user how to type the command correctly.
type UsageError struct {
	Err   error
	Usage string
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// The names that Finish asks explicit about: whether the command line set
// the option itself, so that a default does not override it.
const (
	ExplicitMethod         = "method"
	ExplicitProfileHeaders = "profile-headers"
)

// Finish checks opts and applies the rules that follow from them: the
// chain shortcut, the input checks, the option conflicts, the default
// Content-Type, POST for a body, -o -, and the remote file name. The v0
// flags (ParseFlags) and the v1 parser (internal/cli) both call it, so the
// rules cannot drift. explicit reports whether the command line set an
// option (ExplicitMethod, ExplicitProfileHeaders).
func Finish(opts *Options, explicit func(name string) bool) error {
	applyChainShortcuts(opts)
	if opts.URL == "" {
		return fmt.Errorf("URL is mandatory")
	}
	for _, check := range []func(*Options) error{checkInput, checkOneOutput, checkConflicts, checkValues} {
		if err := check(opts); err != nil {
			return err
		}
	}
	if opts.Stealth.Impersonate != "" {
		if _, err := profile.Resolve(opts.Stealth.Impersonate); err != nil {
			return err
		}
	}
	applyRules(opts, explicit)
	return nil
}

// checkConflicts refuses options that cannot go together.
func checkConflicts(opts *Options) error {
	o := opts.Output
	switch {
	case opts.ProxyCfg.Proxy != "" && opts.ProxyCfg.File != "":
		return fmt.Errorf("--proxy and --proxy-file cannot be used together")
	case o.ContinueAt != "" && o.Range != "":
		return fmt.Errorf("--continue (or --continue-at) and --range cannot be used together")
	case opts.Stealth.Compressed && (o.ContinueAt != "" || o.Range != ""):
		return fmt.Errorf("--compressed cannot be combined with --continue or --range: the byte ranges would apply to the compressed stream")
	case len(opts.Form) > 0 && (opts.Data != "" || opts.DataBinary != "" || len(opts.DataURLEncode) > 0):
		return fmt.Errorf("--form cannot be combined with --data or --field")
	}
	return nil
}

// checkValues checks the values that the parser keeps as text.
func checkValues(opts *Options) error {
	if c := opts.Output.ContinueAt; c != "" && c != "-" {
		if _, err := strconv.ParseInt(c, 10, 64); err != nil {
			return fmt.Errorf("invalid --continue-at value %q (want a byte offset)", c)
		}
	}
	if opts.Output.LimitRate != "" {
		if _, err := ParseRate(opts.Output.LimitRate); err != nil {
			return fmt.Errorf("invalid --rate-max %q: %w", opts.Output.LimitRate, err)
		}
	}
	if r := opts.Stealth.Referer; r != "none" && r != "auto" {
		if _, err := url.ParseRequestURI(r); err != nil {
			return fmt.Errorf("invalid --referer value %q", r)
		}
	}
	return nil
}

// applyRules sets what follows from the options: redirects for
// --follow-trusted, the profile headers of --impersonate, the default
// Content-Type, POST for a body, -o -, and the remote file name.
func applyRules(opts *Options, explicit func(name string) bool) {
	if opts.Resilience.LocationTrusted {
		opts.Resilience.Location = true // curl --location-trusted is -L plus the credentials on every hop
	}
	if opts.Stealth.Impersonate != "" && !explicit(ExplicitProfileHeaders) {
		opts.Stealth.StealthHeaders = true
	}
	addDefaultContentType(opts)
	hasBody := opts.Data != "" || opts.DataBinary != "" || len(opts.Form) > 0 || len(opts.DataURLEncode) > 0
	if hasBody && !explicit(ExplicitMethod) {
		opts.Method = fasthttp.MethodPost
	}
	if opts.Output.File == "-" {
		opts.Output.File, opts.Output.Stdout = "", true
	}
	if opts.Output.RemoteName && opts.Output.File == "" {
		inferWgetFilename(opts)
	}
}

func applyChainShortcuts(opts *Options) {
	if opts.Chain == "" {
		return
	}
	info, ok := LookupChain(opts.Chain)
	if !ok {
		return
	}
	opts.Headers = append(opts.Headers, "X-Chain: "+info.Header)
	// A chain shortcut is for JSON-RPC, so its body is JSON.
	if !hasHeader(opts.Headers, "Content-Type") {
		opts.Headers = append(opts.Headers, jsonContentType)
	}
	if opts.URL == "" && info.DefaultRPC != "" {
		opts.URL = info.DefaultRPC
		opts.URLs = []string{opts.URL}
	}
}

// TakeRemoteName sets the file to the remote name of opts.URL, inside
// -o DIR/, for each URL of a run with several.
func TakeRemoteName(opts *Options) {
	opts.Output.File = ""
	inferWgetFilename(opts)
}

func inferWgetFilename(opts *Options) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return
	}
	name := localname.Safe(u.Path) // the server picks the name: keep it one file in the folder
	if name == "" {
		name = "index.html"
	} else {
		NoteLocalName(opts, localname.Base(u.Path), name)
	}
	opts.Output.File = filepath.Join(opts.Output.Dir, name)
}

// NoteLocalName writes a note when the name that the server chose (raw)
// is saved under another name, so a user who looks for raw finds the file.
func NoteLocalName(opts *Options, raw, saved string) {
	if saved != raw && !opts.Output.Silent {
		fmt.Fprintf(os.Stderr, "Note: the server's file name %q is saved as %q\n", raw, saved)
	}
}

// TakeURLs stores the URLs of the command line, each with http:// when it
// has no scheme. The v1 parser (internal/cli) calls it.
func TakeURLs(opts *Options, args []string) { takeURLs(opts, args) }

// DefaultSpeedTime is curl's --speed-time when only --speed-limit is set.
const DefaultSpeedTime = defaultSpeedTime * time.Second

// ShouldStream reports whether a request should take the streaming download
// path (bounded memory, written to a file) instead of the buffered pipeline.
// The buffered path is required when the whole body must be in memory: JSON
// extraction (--jq), snoop, and HAR export.
func ShouldStream(opts *Options) bool {
	if opts.Output.File == "" {
		return false
	}
	if opts.Output.Snoop || opts.Output.JQ != "" || opts.Output.HAR != "" {
		return false
	}
	return opts.Method == fasthttp.MethodGet
}
