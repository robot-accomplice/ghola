package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/jobs"
)

// recordJob puts the download in the job list, unless --no-session. It
// replaces the entry with the ID old, when the file name changed. It
// returns the ID. A job list that cannot be written is a warning: the
// download itself does not need it.
func recordJob(opts *config.Options, old string) string {
	if opts.Output.NoSession || opts.Output.File == "" {
		return old
	}
	file, id := jobFile(opts), jobID(opts)
	err := jobs.Update(func(m map[string]jobs.Job) {
		added := time.Now()
		if j, ok := m[old]; ok {
			added = j.Added
			delete(m, old)
		}
		m[id] = jobs.Job{ID: id, URL: opts.URL, File: file, Args: opts.Output.SessionArgs, Added: added}
	})
	warnJobs(opts, err)
	return id
}

// jobFile is the absolute path of the output file: the job list and the
// hooks name a file by it, so a job keeps its ID when the folder changes.
func jobFile(opts *config.Options) string {
	if file, err := filepath.Abs(opts.Output.File); err == nil {
		return file
	}
	return opts.Output.File
}

// jobID is the ID of the download in the job list.
func jobID(opts *config.Options) string { return jobs.ID(opts.URL, jobFile(opts)) }

// forgetJob removes a finished download from the job list.
func forgetJob(opts *config.Options, id string) {
	if opts.Output.NoSession || id == "" {
		return
	}
	warnJobs(opts, jobs.Update(func(m map[string]jobs.Job) { delete(m, id) }))
}

func warnJobs(opts *config.Options, err error) {
	if err != nil && !opts.Output.Silent {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
}

// runJobsCommand runs "ghola jobs [list|resume|pause|cancel|clean] [ID...]".
func runJobsCommand(args []string, w *os.File) int {
	verb, ids := "list", args
	if len(args) > 0 {
		verb, ids = args[0], args[1:]
	}
	list, err := jobs.List()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return config.ReadFileFailed.Int()
	}
	switch verb {
	case "list":
		for _, j := range list {
			state := "unfinished"
			if j.Paused {
				state = "paused"
			}
			size := int64(0)
			if fi, err := os.Stat(j.File); err == nil {
				size = fi.Size()
			}
			fmt.Fprintf(w, "%s  %-10s  %10d  %s  %s\n", j.ID, state, size, j.File, j.URL)
		}
		return config.NoError.Int()
	case "resume":
		return resumeJobs(list, ids, w)
	case "pause", "cancel":
		return changeJobs(verb, ids)
	case "clean":
		return cleanJobs()
	}
	return argError(fmt.Errorf("ghola jobs %s: use list, resume, pause, cancel or clean", verb))
}

// resumeJobs restarts the given jobs, or every job that is not paused,
// with the options of its first run and -c. The exit code is the last
// failure's, as for several URLs.
func resumeJobs(list []jobs.Job, ids []string, w *os.File) int {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	last := config.NoError.Int()
	for _, j := range list {
		if (len(want) > 0 && !want[j.ID]) || (len(want) == 0 && j.Paused) {
			continue
		}
		args := append(append([]string{"download"}, j.Args...), "-c", "-o", j.File, j.URL)
		if code := execute(context.Background(), args, nil, nil, w); code != config.NoError.Int() {
			last = code
		}
	}
	return last
}

// changeJobs pauses jobs, or cancels them: a cancel also removes the
// partial file and its state file.
func changeJobs(verb string, ids []string) int {
	if len(ids) == 0 {
		return argError(fmt.Errorf("ghola jobs %s needs a job ID; ghola jobs list shows them", verb))
	}
	var missing, running []string
	err := jobs.Update(func(m map[string]jobs.Job) {
		for _, id := range ids {
			j, ok := m[id]
			switch {
			case !ok:
				missing = append(missing, id)
			case config.InUse(j.File):
				running = append(running, id) // only the job list changes here: a running transfer would go on
			case verb == "pause":
				j.Paused = true
				m[id] = j
			default:
				os.Remove(j.File)            //nolint:errcheck // the file may not exist yet
				os.Remove(j.File + ".ghola") //nolint:errcheck
				delete(m, id)
			}
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return config.WriteFileFailed.Int()
	}
	if len(missing) > 0 {
		return argError(fmt.Errorf("no job %v; ghola jobs list shows the jobs", missing))
	}
	if len(running) > 0 {
		fmt.Fprintf(os.Stderr, "ghola jobs %s: job %v is running; stop its ghola first (Ctrl+C), then run this again\n", verb, running)
		return config.WriteFileFailed.Int()
	}
	return config.NoError.Int()
}

// cleanJobs removes the entries whose file is gone. A download that ends
// well leaves the list by itself; a partial file without an ETag or a
// Last-Modified has no state file, so that cannot mark a finished one.
func cleanJobs() int {
	err := jobs.Update(func(m map[string]jobs.Job) {
		for id, j := range m {
			if _, err := os.Stat(j.File); err != nil {
				delete(m, id)
			}
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return config.WriteFileFailed.Int()
	}
	return config.NoError.Int()
}
