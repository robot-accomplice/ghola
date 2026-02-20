// Package config handles CLI flag parsing, validation, and the Options type
// that carries all user-specified settings through the ghola pipeline.
package config

import (
	"fmt"
	"net/url"
	"path"
	"strings"

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

// Options holds every user-configurable setting parsed from CLI flags.
type Options struct {
	URL         string
	Method      string
	Data        string
	Output      string
	Transfer    string
	Headers     []string
	Verbose     bool
	Fail        bool
	Include     bool
	Silent      bool
	User        string
	Agent       string
	WgetMode    bool
	Concurrency int
	Drift       int
	Ghost       bool
	Retries     int
	Backoff     int
	Snoop       bool
	Chain       string
	Serve       bool
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
	fs.BoolVarP(&help, "help", "h", false, "help")
	fs.StringVarP(&opts.Data, "data", "d", "", "HTTP POST data")
	fs.BoolVarP(&opts.Fail, "fail", "f", false, "Fail silently on HTTP errors")
	fs.BoolVarP(&opts.Include, "include", "i", false, "Include protocol response headers")
	fs.StringVarP(&opts.Output, "output", "o", "", "Write to file instead of stdout")
	fs.BoolVarP(&opts.WgetMode, "wget", "w", false, "Enable wget-compatible output")
	fs.IntVarP(&opts.Concurrency, "connections", "n", 1, "Number of concurrent connections")
	fs.BoolVarP(&opts.Silent, "silent", "s", false, "Silent mode")
	fs.StringVarP(&opts.Transfer, "upload-file", "T", "", "Transfer local FILE to destination")
	fs.StringVarP(&opts.User, "user", "u", "", "Server user and password <user:password>")
	fs.StringVarP(&opts.Agent, "user-agent", "A", "ghola", "Send User-Agent <name>")
	fs.StringVarP(&opts.Method, "request", "X", "GET", "HTTP request method")
	fs.StringSliceVarP(&opts.Headers, "header", "H", []string{"Content-Type: application/json"}, "Pass custom headers")
	fs.BoolVarP(&opts.Verbose, "verbose", "v", false, "verbose mode")

	fs.IntVarP(&opts.Drift, "drift", "D", 0, "Temporal Drift (ms jitter)")
	fs.BoolVarP(&opts.Ghost, "ghost", "G", false, "Ghost Sign (unique identity hash)")
	fs.IntVarP(&opts.Retries, "retry", "r", 0, "Number of retries on failure")
	fs.IntVarP(&opts.Backoff, "backoff", "b", 1000, "Base backoff time for retries (ms)")
	fs.BoolVarP(&opts.Snoop, "snoop", "S", false, "Snoop Mode: Pre-flight check of headers and security posture")
	fs.StringVarP(&opts.Chain, "chain", "c", "", "Chain-Aware Shortcut: Pre-fills RPC headers for [eth, base, solana]")
	fs.BoolVar(&opts.Serve, "serve", false, "Run as local RPC bridge on 127.0.0.1:18789")

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

	if fs.NArg() > 0 {
		opts.URL = fs.Arg(0)
	}

	if opts.URL == "" && !opts.Serve {
		return nil, false, fmt.Errorf("URL is mandatory")
	}

	applyChainShortcuts(opts)

	if opts.Data != "" && !fs.Changed("request") {
		opts.Method = fasthttp.MethodPost
	}

	if opts.WgetMode && opts.Output == "" {
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
	opts.Output = path.Base(u.Path)
	if opts.Output == "." || opts.Output == "/" {
		opts.Output = "index.html"
	}
}
