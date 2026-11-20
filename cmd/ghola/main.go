// Command ghola is a high-performance HTTP client for blockchain forensics
// and stealthy data acquisition. See https://github.com/robot-accomplice/ghola.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/robot-accomplice/ghola/internal/bridge"
	"github.com/robot-accomplice/ghola/internal/cli"
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/output"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], nil, nil, os.Stdout))
}

func execute(ctx context.Context, args []string, do client.Doer, stream client.Streamer, w *os.File) int {
	c, err := cli.Parse(args)
	if err != nil {
		return argError(err)
	}
	if code, done := runJob(c, w); done {
		return code
	}
	opts, err := cli.Apply(c)
	if err != nil {
		return argError(err)
	}
	if c.Job == cli.JobInspect {
		opts.Output.Snoop = true
	}
	return transfer(ctx, opts, do, stream, w)
}

// argError reports an argument error with the short usage around it, as
// Jon decided on 2026-11-12, and returns exit 2.
func argError(err error) int {
	reportArgError(os.Stderr, &config.UsageError{Err: err, Usage: cli.ShortUsage(config.Version)}, useColor(os.Stderr))
	return config.BadFlag.Int()
}

// runJob runs --help, --version and the jobs that do not transfer. done is
// false for fetch, download and inspect, which transfer.
func runJob(c *cli.Command, w *os.File) (code int, done bool) {
	switch {
	case c.Has("help"):
		text, err := cli.Help(config.Version, c.Last("help"))
		if err != nil {
			return argError(err), true
		}
		fmt.Fprint(w, text)
		return config.NoError.Int(), true
	case c.Has("version"):
		fmt.Fprintf(w, "ghola version %s\n", config.Version)
		return config.NoError.Int(), true
	}
	switch c.Job {
	case cli.JobFetch, cli.JobDownload, cli.JobInspect:
		return 0, false
	case cli.JobServe:
		if err := bridge.ListenAndServe(bridge.Addr); err != nil {
			fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
			return config.SendFailed.Int(), true
		}
		return config.NoError.Int(), true
	case cli.JobProfiles:
		for _, name := range profile.ProfileNames() {
			fmt.Fprintln(w, name)
		}
		return config.NoError.Int(), true
	case cli.JobExplain:
		if len(c.Args) != 1 {
			return argError(fmt.Errorf("ghola explain takes one exit code, such as 28")), true
		}
		text, err := cli.Explain(c.Args[0])
		if err != nil {
			return argError(err), true
		}
		fmt.Fprint(w, text)
		return config.NoError.Int(), true
	default:
		return argError(fmt.Errorf("ghola %s is in the v1 spec but not built yet", c.Job)), true
	}
}

// transfer reads the request body sources and fetches each URL.
func transfer(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {

	if opts.Transfer != "" {
		b, err := os.ReadFile(opts.Transfer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read upload file: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		opts.Data = string(b)
	}

	// Stdin/file substitution: -d - reads stdin, -d @file reads from file.
	if opts.DataLiteral {
		// --data-literal: the value is the body, also when it starts with @.
	} else if opts.Data == "-" || opts.Data == "@-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		opts.Data = string(b)
	} else if len(opts.Data) > 1 && opts.Data[0] == '@' {
		b, err := os.ReadFile(opts.Data[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "read data file: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		opts.Data = string(b)
	}

	if len(opts.Form) > 0 {
		ct, body, err := config.BuildFormBody(opts.Form)
		if err != nil {
			fmt.Fprintf(os.Stderr, "form: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		// Strip any existing Content-Type header (default is application/json)
		// so the multipart boundary header is the only one.
		opts.Headers = slices.DeleteFunc(opts.Headers, func(h string) bool {
			return strings.HasPrefix(strings.ToLower(strings.TrimSpace(h)), "content-type:")
		})
		opts.Headers = append(opts.Headers, "Content-Type: "+ct)
		opts.Data = string(body)
	} else if opts.DataBinary != "" {
		data := opts.DataBinary
		if strings.HasPrefix(data, "@") {
			b, err := os.ReadFile(data[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "read --data-binary file: %v\n", err)
				return config.ReadFileFailed.Int()
			}
			data = string(b)
		}
		opts.Data = data
	} else if len(opts.DataURLEncode) > 0 {
		opts.Data = config.URLEncodeData(opts.DataURLEncode)
	}

	// Several URLs go one after the other, as in curl (audit finding M9).
	// The first failure stops the run and gives the exit code.
	for _, u := range opts.URLs {
		one := *opts
		one.URL = u
		if code := fetch(ctx, &one, do, stream, w); code != config.NoError.Int() {
			return code
		}
	}
	return config.NoError.Int()
}

// fetch sends the request for opts.URL and writes the response. It returns
// the exit code. --max-time covers each URL on its own, as in curl.
func fetch(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {
	ctx, cancel := client.WithTimeout(ctx, opts)
	defer cancel()

	if config.ShouldStream(opts) {
		if err := client.Download(ctx, opts, stream); err != nil {
			if !opts.Output.HideErrors {
				fmt.Fprintf(os.Stderr, "Failed: %s\n", err)
			}
			return exitFor(err).Int()
		}
		return config.NoError.Int()
	}

	// -n splits a download to a file into ranges. It does not repeat a
	// request: n copies of a POST are a side effect (audit finding H4).
	if opts.Concurrency > 1 && !opts.Output.Silent {
		fmt.Fprintln(os.Stderr, "Note: -n applies only to a download to a file (-o); sending one request")
	}

	start := time.Now()
	req, rsp, err := client.FetchURL(ctx, opts, do)
	elapsed := time.Since(start)
	if err != nil {
		if !opts.Output.HideErrors {
			fmt.Fprintf(os.Stderr, "Failed: %s\n", err)
		}
		return exitFor(err).Int()
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if opts.Output.Snoop {
		output.RunSnoop(w, opts, rsp)
		return config.NoError.Int()
	}

	// The HAR file records the body, so read it before stdout gets it.
	if opts.Output.HAR != "" {
		if _, err := gholatransport.ReadBody(rsp); err != nil {
			fmt.Fprintf(os.Stderr, "read response body: %v\n", err)
			return exitFor(err).Int()
		}
	}

	if err := output.ProcessResponse(w, os.Stderr, opts, req, rsp, elapsed); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFor(err).Int()
	}

	if opts.Output.HAR != "" {
		if err := output.WriteHAR(opts.Output.HAR, opts, req, rsp, start, elapsed); err != nil {
			fmt.Fprintf(os.Stderr, "har: %v\n", err)
		}
	}

	return config.NoError.Int()
}
