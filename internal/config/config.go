// Package config handles CLI flag parsing, validation, and the Options type
// that carries all user-specified settings through the ghola pipeline.
package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/profile"
	flag "github.com/spf13/pflag"
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
	// PeerFailedVerification indicates that the server certificate or its
	// host name did not verify (curl 60).
	PeerFailedVerification ExitCode = 60
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
	File         string
	Verbose      bool
	Fail         bool
	Include      bool
	Silent       bool
	Snoop        bool
	WgetMode     bool
	JQ           string
	Timing       bool
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
	Ghost          bool
	Drift          int
	Agent          string
	AgentExplicit  bool
	Referer        string
	AcceptLanguage string
	HTTP3          bool
	CookieJar      string
	Cookies        []string
	ProfileList    bool
	Compressed     bool // wired in Task 9; true disables segmentation
	Insecure       bool
	CACert         string
	ClientCert     string
	ClientKey      string
}

// ResilienceOptions controls retry, timeout, redirect, and buffer behavior.
type ResilienceOptions struct {
	Timeout   int
	Retries   int
	Backoff   int
	RetryHTTP bool
	Location  bool
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
	Concurrency int
	Chain       string
	Serve       bool

	Output     OutputOptions
	Stealth    StealthOptions
	Resilience ResilienceOptions
	ProxyCfg   ProxyOptions
}

// Version is set at build time via -ldflags.
var Version = "dev"

const banner = `Ghola - a high-performance, Go-based HTTP client designed as a tactical scout for blockchain forensic analysis and stealthy data acquisition.`

// UsageError is an argument error. It carries the usage text so the CLI can
// show the user how to type the command correctly.
type UsageError struct {
	Err   error
	Usage string
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// writeUsage writes the usage text that --help prints.
func writeUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, banner)
	fmt.Fprintf(w, "ghola version %s\n", Version)
	fmt.Fprintln(w, "Usage: ghola [options...] <url>")
	fmt.Fprint(w, fs.FlagUsages())
	fmt.Fprintln(w, "\nNote: ghola's -c (--chain), -G (--ghost), and -b (--backoff) differ")
	fmt.Fprintln(w, "from curl's -c/-G/-b. Resume is -C/--continue-at.")
}

// ParseFlags parses CLI arguments into an Options struct.
// It returns the parsed options, a boolean indicating whether the program
// should exit cleanly (e.g. --help was requested), and any validation error.
// Every error it returns is a *UsageError.
func ParseFlags(args []string) (opts *Options, done bool, err error) {
	opts = &Options{}
	fs := flag.NewFlagSet("ghola", flag.ContinueOnError)
	defer func() {
		if err != nil {
			var usage strings.Builder
			writeUsage(&usage, fs)
			err = &UsageError{Err: err, Usage: usage.String()}
		}
	}()

	var help bool
	var version bool
	fs.BoolVarP(&help, "help", "h", false, "help")
	fs.BoolVarP(&version, "version", "V", false, "version")
	fs.StringVarP(&opts.Data, "data", "d", "", "HTTP POST data")
	fs.StringArrayVarP(&opts.Form, "form", "F", nil, "Specify multipart form data (name=value or name=@file)")
	fs.StringVar(&opts.DataBinary, "data-binary", "", "HTTP POST binary data (no munging); @file reads verbatim")
	fs.StringArrayVar(&opts.DataURLEncode, "data-urlencode", nil, "URL-encode and POST the given data")
	fs.BoolVarP(&opts.Output.Fail, "fail", "f", false, "Exit non-zero on non-2xx and suppress body output")
	fs.BoolVarP(&opts.Output.Include, "include", "i", false, "Include protocol response headers")
	fs.StringVarP(&opts.Output.File, "output", "o", "", "Write to file instead of stdout")
	fs.BoolVarP(&opts.Output.WgetMode, "wget", "w", false, "Enable wget-compatible output")
	fs.IntVarP(&opts.Concurrency, "connections", "n", DefaultConcurrency, "Number of concurrent connections")
	fs.BoolVarP(&opts.Output.Silent, "silent", "s", false, "Silent mode")
	fs.StringVarP(&opts.Transfer, "upload-file", "T", "", "Transfer local FILE to destination")
	fs.StringVarP(&opts.User, "user", "u", "", "Server user and password <user:password>")
	fs.StringVarP(&opts.Stealth.Agent, "user-agent", "A", DefaultAgent, "Send User-Agent <name>")
	fs.StringVarP(&opts.Method, "request", "X", DefaultMethod, "HTTP request method")
	fs.StringSliceVarP(&opts.Headers, "header", "H", nil, "Pass custom headers")
	fs.BoolVarP(&opts.Output.Verbose, "verbose", "v", false, "verbose mode")

	fs.IntVarP(&opts.Stealth.Drift, "drift", "D", 0, "Temporal Drift (ms jitter)")
	fs.BoolVarP(&opts.Stealth.Ghost, "ghost", "G", false, "Ghost Sign (unique identity hash)")
	fs.IntVar(&opts.Resilience.Timeout, "timeout", 0, "Request timeout in ms (0 disables)")
	fs.IntVarP(&opts.Resilience.Retries, "retry", "r", 0, "Number of retries on failure")
	fs.IntVarP(&opts.Resilience.Backoff, "backoff", "b", DefaultBackoffMs, "Base backoff time for retries (ms)")
	fs.BoolVar(&opts.Resilience.RetryHTTP, "retry-http", false, "Retry on retryable HTTP status codes (e.g. 429, 5xx)")
	fs.BoolVarP(&opts.Resilience.Location, "location", "L", false, "Follow HTTP redirects")
	fs.BoolVar(&opts.Resilience.LocationTrusted, "location-trusted", false, "Like -L, and send -u, Authorization and Cookie to other hosts too")
	fs.IntVar(&opts.Resilience.MaxRedirs, "max-redirs", DefaultMaxRedirs, "Maximum number of redirects to follow")
	fs.BoolVarP(&opts.Output.Snoop, "snoop", "S", false, "Snoop Mode: Pre-flight check of headers and security posture")
	fs.StringVarP(&opts.Chain, "chain", "c", "", "Chain-Aware Shortcut: Pre-fills RPC headers for [eth, base, solana]")
	fs.BoolVar(&opts.Serve, "serve", false, "Run as local RPC bridge on 127.0.0.1:18789")
	fs.IntVar(&opts.Resilience.BufferSize, "buffer-size", DefaultBufferSize, "Read buffer size for large headers")
	fs.StringVar(&opts.Stealth.Impersonate, "impersonate", "", "Use a browser-like request profile (chrome, firefox, safari, edge)")
	fs.BoolVar(&opts.Stealth.StealthHeaders, "stealth-headers", false, "Generate coherent browser-like headers")
	fs.BoolVar(&opts.Stealth.HTTP3, "http3", false, "Attempt HTTP/3 when supported by the active transport")
	fs.StringVar(&opts.Stealth.CookieJar, "cookie-jar", "", "Persist cookies to a JSON jar file")
	fs.StringSliceVar(&opts.Stealth.Cookies, "cookie", nil, "Seed request cookies as name=value")
	fs.StringVar(&opts.ProxyCfg.Proxy, "proxy", "", "Route traffic through an HTTP proxy URL")
	fs.StringVar(&opts.ProxyCfg.Auth, "proxy-auth", "", "Proxy credentials in user:pass form")
	fs.StringVar(&opts.ProxyCfg.File, "proxy-file", "", "Load proxies from a newline-delimited file")
	fs.StringVar(&opts.ProxyCfg.Strategy, "proxy-strategy", DefaultProxyStrategy, "Proxy selection strategy: sticky, random, round-robin")
	fs.BoolVar(&opts.ProxyCfg.HealthCheck, "proxy-check", false, "Check proxy health before use and evict dead proxies")
	fs.StringVar(&opts.Stealth.Referer, "referer", DefaultReferer, "Referer behavior: none, auto, or an explicit URL")
	fs.StringVar(&opts.Stealth.AcceptLanguage, "accept-language", "", "Override the profile default Accept-Language")
	fs.BoolVar(&opts.Stealth.ProfileList, "profile-list", false, "List available browser-like profiles and exit")
	fs.StringVar(&opts.Output.JQ, "jq", "", "Extract a JSON path from the response body (gjson syntax)")
	fs.BoolVar(&opts.Output.Timing, "timing", false, "Show response timing information")
	fs.StringVar(&opts.Output.HAR, "har", "", "Export request/response as HAR 1.2 to a file")
	fs.StringVarP(&opts.Output.ContinueAt, "continue-at", "C", "", "Resume a partial download at OFFSET, or '-' for auto")
	fs.StringVar(&opts.Output.Range, "range", "", "Request only a byte RANGE (e.g. 0-1023)")
	fs.BoolVarP(&opts.Output.RemoteName, "remote-name", "O", false, "Write output to a file named like the remote file")
	fs.BoolVarP(&opts.Output.RemoteHeader, "remote-header-name", "J", false, "Use Content-Disposition filename for -O")
	fs.BoolVarP(&opts.Output.RemoteTime, "remote-time", "R", false, "Set the local file timestamp to the remote one")
	fs.StringVar(&opts.Output.LimitRate, "limit-rate", "", "Limit transfer rate (e.g. 500k, 2M, 1g)")
	fs.BoolVar(&opts.Stealth.Compressed, "compressed", false, "Request a gzip-compressed response and decode it")
	fs.BoolVarP(&opts.Stealth.Insecure, "insecure", "k", false, "Allow insecure server connections (skip TLS verification)")
	fs.StringVar(&opts.Stealth.CACert, "cacert", "", "CA certificate file to verify the peer")
	fs.StringVar(&opts.Stealth.ClientCert, "cert", "", "Client certificate file")
	fs.StringVar(&opts.Stealth.ClientKey, "key", "", "Private key file for the client certificate")

	var limitFlags curlLimitFlags
	limitFlags.register(fs)

	fs.Usage = func() { writeUsage(os.Stdout, fs) }

	if err := fs.Parse(args); err != nil {
		return nil, false, err
	}

	if help {
		fs.Usage()
		return opts, true, nil
	}

	if version {
		fmt.Printf("ghola version %s\n", Version)
		return opts, true, nil
	}

	if opts.Stealth.ProfileList {
		for _, name := range profile.ProfileNames() {
			fmt.Println(name)
		}
		return opts, true, nil
	}

	takeURLs(opts, fs.Args())
	opts.Stealth.AgentExplicit = fs.Changed("user-agent")
	if err := limitFlags.apply(fs, &opts.Resilience); err != nil {
		return nil, false, err
	}
	explicit := map[string]bool{ExplicitMethod: fs.Changed("request"), ExplicitProfileHeaders: fs.Changed("stealth-headers")}
	if err := Finish(opts, func(name string) bool { return explicit[name] }); err != nil {
		return nil, false, err
	}
	return opts, false, nil
}

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
	if opts.URL == "" && !opts.Serve {
		return fmt.Errorf("URL is mandatory")
	}
	if err := checkInput(opts); err != nil {
		return err
	}
	if err := checkOneOutput(opts); err != nil {
		return err
	}
	// curl --location-trusted is -L plus the credentials on every hop.
	if opts.Resilience.LocationTrusted {
		opts.Resilience.Location = true
	}

	if opts.Stealth.Impersonate != "" {
		if _, err := profile.Resolve(opts.Stealth.Impersonate); err != nil {
			return err
		}
		if !explicit(ExplicitProfileHeaders) {
			opts.Stealth.StealthHeaders = true
		}
	}

	if opts.ProxyCfg.Proxy != "" && opts.ProxyCfg.File != "" {
		return fmt.Errorf("--proxy and --proxy-file cannot be used together")
	}
	if opts.Output.ContinueAt != "" && opts.Output.Range != "" {
		return fmt.Errorf("--continue-at and --range cannot be used together")
	}
	if opts.Stealth.Compressed && opts.Output.ContinueAt != "" {
		return fmt.Errorf("--compressed cannot be combined with --continue-at (byte ranges apply to the compressed stream)")
	}
	if opts.Stealth.Compressed && opts.Output.Range != "" {
		return fmt.Errorf("--compressed cannot be combined with --range (byte ranges apply to the compressed stream)")
	}
	if opts.Output.ContinueAt != "" && opts.Output.ContinueAt != "-" {
		if _, err := strconv.ParseInt(opts.Output.ContinueAt, 10, 64); err != nil {
			return fmt.Errorf("invalid --continue-at value %q (want an integer offset or '-')", opts.Output.ContinueAt)
		}
	}
	if opts.Output.LimitRate != "" {
		if _, err := ParseRate(opts.Output.LimitRate); err != nil {
			return fmt.Errorf("invalid --limit-rate %q: %w", opts.Output.LimitRate, err)
		}
	}
	if opts.Stealth.Referer != "none" && opts.Stealth.Referer != "auto" {
		if _, err := url.ParseRequestURI(opts.Stealth.Referer); err != nil {
			return fmt.Errorf("invalid --referer value %q", opts.Stealth.Referer)
		}
	}

	if len(opts.Form) > 0 && (opts.Data != "" || opts.DataBinary != "") {
		return fmt.Errorf("--form cannot be combined with -d/--data-binary")
	}
	if len(opts.Form) > 0 && len(opts.DataURLEncode) > 0 {
		return fmt.Errorf("--form cannot be combined with --data-urlencode")
	}

	addDefaultContentType(opts)
	if (opts.Data != "" || opts.DataBinary != "" || len(opts.Form) > 0 || len(opts.DataURLEncode) > 0) && !explicit(ExplicitMethod) {
		opts.Method = fasthttp.MethodPost
	}

	if opts.Output.File == "-" {
		opts.Output.File, opts.Output.Stdout = "", true
	}
	if opts.Output.WgetMode && opts.Output.File == "" {
		inferWgetFilename(opts)
	}
	if opts.Output.RemoteName && opts.Output.File == "" {
		inferWgetFilename(opts)
	}

	return nil
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

func inferWgetFilename(opts *Options) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" {
		name = "index.html"
	}
	opts.Output.File = filepath.Join(opts.Output.Dir, name)
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
