// Command ghola is a high-performance HTTP client for blockchain forensics
// and stealthy data acquisition. See https://github.com/robot-accomplice/ghola.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/robot-accomplice/ghola/internal/bridge"
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/output"
	"github.com/valyala/fasthttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], nil, os.Stdout))
}

func execute(ctx context.Context, args []string, do client.Doer, w *os.File) int {
	opts, done, err := config.ParseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return config.BadFlag.Int()
	}
	if done {
		return config.NoError.Int()
	}

	if opts.Serve {
		if err := bridge.ListenAndServe(bridge.Addr); err != nil {
			fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
			return config.SendFailed.Int()
		}
		return config.NoError.Int()
	}

	if opts.Transfer != "" {
		b, err := os.ReadFile(opts.Transfer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read upload file: %v\n", err)
			return config.ReadFileFailed.Int()
		}
		opts.Data = string(b)
	}

	if opts.Concurrency > 1 {
		client.RunConcurrent(ctx, opts, do, func(req *fasthttp.Request, rsp *fasthttp.Response) {
			defer fasthttp.ReleaseRequest(req)
			defer fasthttp.ReleaseResponse(rsp)
			if opts.Output.Snoop {
				output.RunSnoop(w, opts, rsp)
			} else {
				if err := output.ProcessResponse(w, opts, req, rsp); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
		})
		return config.NoError.Int()
	}

	req, rsp, err := client.FetchURL(ctx, opts, do)
	if err != nil {
		if !opts.Output.Silent {
			fmt.Fprintf(os.Stderr, "Failed: %s\n", err)
		}
		return config.SendFailed.Int()
	}
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(rsp)

	if opts.Output.Snoop {
		output.RunSnoop(w, opts, rsp)
		return config.NoError.Int()
	}

	if err := output.ProcessResponse(w, opts, req, rsp); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, output.ErrNon2xx) {
			return config.SendFailed.Int()
		}
		return config.WriteFileFailed.Int()
	}

	return config.NoError.Int()
}
