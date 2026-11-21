package main

import (
	"context"
	"os"

	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	gholamirror "github.com/robot-accomplice/ghola/internal/mirror"
)

// runMirror copies the site of each start URL, as wget -m does, into
// -o DIR/ or the current folder. The exit code is the last failure's, as
// for several URLs.
func runMirror(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer) int {
	root := opts.Output.Dir
	if root == "" {
		root = "."
	}
	var log *os.File
	if !opts.Output.Silent {
		log = os.Stderr
	}
	last := config.NoError.Int()
	for _, u := range opts.URLs {
		one := *opts
		one.URL, one.URLs = u, []string{u}
		m := &gholamirror.Mirror{Base: &one, Opts: opts.Mirror, Root: root, Do: do, Stream: stream,
			ExitCode: func(err error) int { return exitFor(err).Int() }}
		if log != nil {
			m.Log = log
		}
		if err := m.Run(ctx); err != nil {
			last = exitFor(err).Int()
		}
	}
	return last
}
