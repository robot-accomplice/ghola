// Package config handles CLI flag parsing, validation, and the Options type
// that carries all user-specified settings through the ghola pipeline.
package config

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/robot-accomplice/ghola/internal/profile"
	flag "github.com/spf13/pflag"
	"github.com/valyala/fasthttp"
)

// ExitCode represents a process exit status returned by ghola.
type ExitCode int

const (
	// NoError indicates successful execution.
	NoError ExitCode = iota
	// BadFlag indicates invalid or missing CLI arguments.
	BadFlag
	// SendFailed indicates the HTTP request could not be completed.
	SendFailed
	// ReadFileFailed indicates a local file could not be read (e.g. --upload-file).
	ReadFileFailed
	// WriteFileFailed indicates output could not be written to disk.
	WriteFileFailed
)

// Int returns the ExitCode as a plain int for use with os.Exit.
func (e ExitCode) Int() int {
	return int(e)
}

// OutputOptions controls how HTTP responses are rendered.
type OutputOptions struct {
	File     string
	Verbose  bool
	Fail     bool
	Include  bool
	Silent   bool
	Snoop    bool
	WgetMode bool
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
}

// ResilienceOptions controls retry, timeout, redirect, and buffer behavior.
type ResilienceOptions struct {
	Timeout    int
	Retries    int
	Backoff    int
	RetryHTTP  bool
	Location   bool
	MaxRedirs  int
	BufferSize int
}

// ProxyOptions controls proxy routing behavior.
type ProxyOptions struct {
	Proxy    string
	Auth     string
	File     string
	Strategy string
}

// Options holds every user-configurable setting parsed from CLI flags.
type Options struct {
	URL         string
	Method      string
	Data        string
	Headers     []string
	Transfer    string
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

// ParseFlags parses CLI arguments into an Options struct.
// It returns the parsed options, a boolean indicating whether the program
// should exit cleanly (e.g. --help was requested), and any validation error.
func ParseFlags(args []string) (*Options, bool, error) {
	opts := &Options{}
	fs := flag.NewFlagSet("ghola", flag.ContinueOnError)

	var help bool
	var version bool
	fs.BoolVarP(&help, "help", "h", false, "help")
	fs.BoolVarP(&version, "version", "V", false, "version")
	fs.StringVarP(&opts.Data, "data", "d", "", "HTTP POST data")
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
	fs.StringSliceVarP(&opts.Headers, "header", "H", []string{DefaultContentType}, "Pass custom headers")
	fs.BoolVarP(&opts.Output.Verbose, "verbose", "v", false, "verbose mode")

	fs.IntVarP(&opts.Stealth.Drift, "drift", "D", 0, "Temporal Drift (ms jitter)")
	fs.BoolVarP(&opts.Stealth.Ghost, "ghost", "G", false, "Ghost Sign (unique identity hash)")
	fs.IntVar(&opts.Resilience.Timeout, "timeout", 0, "Request timeout in ms (0 disables)")
	fs.IntVarP(&opts.Resilience.Retries, "retry", "r", 0, "Number of retries on failure")
	fs.IntVarP(&opts.Resilience.Backoff, "backoff", "b", DefaultBackoffMs, "Base backoff time for retries (ms)")
	fs.BoolVar(&opts.Resilience.RetryHTTP, "retry-http", false, "Retry on retryable HTTP status codes (e.g. 429, 5xx)")
	fs.BoolVarP(&opts.Resilience.Location, "location", "L", false, "Follow HTTP redirects")
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
	fs.StringVar(&opts.Stealth.Referer, "referer", DefaultReferer, "Referer behavior: none, auto, or an explicit URL")
	fs.StringVar(&opts.Stealth.AcceptLanguage, "accept-language", "", "Override the profile default Accept-Language")
	fs.BoolVar(&opts.Stealth.ProfileList, "profile-list", false, "List available browser-like profiles and exit")

	fs.Usage = func() {
		fmt.Print(banner)
		fmt.Printf("ghola version %s\n", Version)
		fmt.Println("Usage: ghola [options...] <url>")
		fs.PrintDefaults()
	}

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

	if fs.NArg() > 0 {
		opts.URL = fs.Arg(0)
	}

	if opts.URL == "" && !opts.Serve {
		return nil, false, fmt.Errorf("URL is mandatory")
	}

	applyChainShortcuts(opts)
	opts.Stealth.AgentExplicit = fs.Changed("user-agent")

	if opts.Stealth.Impersonate != "" {
		if _, err := profile.Resolve(opts.Stealth.Impersonate); err != nil {
			return nil, false, err
		}
		if !fs.Changed("stealth-headers") {
			opts.Stealth.StealthHeaders = true
		}
	}

	if opts.ProxyCfg.Proxy != "" && opts.ProxyCfg.File != "" {
		return nil, false, fmt.Errorf("--proxy and --proxy-file cannot be used together")
	}
	if opts.Stealth.Referer != "none" && opts.Stealth.Referer != "auto" {
		if _, err := url.ParseRequestURI(opts.Stealth.Referer); err != nil {
			return nil, false, fmt.Errorf("invalid --referer value %q", opts.Stealth.Referer)
		}
	}

	if opts.Data != "" && !fs.Changed("request") {
		opts.Method = fasthttp.MethodPost
	}

	if opts.Output.WgetMode && opts.Output.File == "" {
		inferWgetFilename(opts)
	}

	return opts, false, nil
}

func applyChainShortcuts(opts *Options) {
	switch strings.ToLower(opts.Chain) {
	case "eth", "ethereum":
		opts.Headers = append(opts.Headers, "X-Chain: ethereum")
	case "base":
		opts.Headers = append(opts.Headers, "X-Chain: base")
	case "solana", "sol":
		opts.Headers = append(opts.Headers, "X-Chain: solana")
	}
}

func inferWgetFilename(opts *Options) {
	u, err := url.Parse(opts.URL)
	if err != nil {
		return
	}
	opts.Output.File = path.Base(u.Path)
	if opts.Output.File == "." || opts.Output.File == "/" {
		opts.Output.File = "index.html"
	}
}
