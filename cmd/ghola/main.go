// Command ghola is a high-performance HTTP client for blockchain forensics
// and stealthy data acquisition. See https://github.com/robot-accomplice/ghola.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/bridge"
	"github.com/robot-accomplice/ghola/internal/cli"
	"github.com/robot-accomplice/ghola/internal/client"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/glob"
	"github.com/robot-accomplice/ghola/internal/output"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"golang.org/x/term"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], nil, nil, os.Stdout))
}

// execute runs the command line. With --next it runs each group in turn,
// as curl does, and the exit code is the last failure's (v1 defaults,
// item 10B). Every group parses before the first transfer, so a typo in
// a later group stops the run before anything is sent.
func execute(ctx context.Context, args []string, do client.Doer, stream client.Streamer, w *os.File) int {
	groups, err := cli.SplitNext(args)
	if err != nil {
		return argError(err)
	}
	return runGroups(ctx, groups, false, do, stream, w)
}

// runGroups runs each group in turn. hideErrors is curl -s of a
// compatibility command.
func runGroups(ctx context.Context, groups [][]string, hideErrors bool, do client.Doer, stream client.Streamer, w *os.File) int {
	if len(groups) == 1 {
		return executeOne(ctx, groups[0], hideErrors, do, stream, w)
	}
	for _, g := range groups {
		if _, err := cli.Parse(g); err != nil {
			return argError(err)
		}
	}
	last := config.NoError.Int()
	for _, g := range groups {
		if code := executeOne(ctx, g, hideErrors, do, stream, w); code != config.NoError.Int() {
			last = code
		}
	}
	return last
}

// executeOne runs one group of options and URLs.
func executeOne(ctx context.Context, args []string, hideErrors bool, do client.Doer, stream client.Streamer, w *os.File) int {
	c, err := cli.Parse(args)
	if err != nil {
		return argError(err)
	}
	if c, err = cli.WithConfig(args, c); err != nil {
		return argError(err)
	}
	if from := compatCommands[c.Job]; from != nil {
		native, err := from(c.Args)
		if err != nil {
			return argError(err)
		}
		if native.ShowNative {
			fmt.Fprintln(w, "ghola "+cli.ShellQuote(native.Args))
			return config.NoError.Int()
		}
		if groups, err := cli.SplitNext(native.Args); err != nil || len(groups) > 1 {
			if err != nil {
				return argError(err)
			}
			return runGroups(ctx, groups, native.HideErrors, do, stream, w) // curl --next
		}
		if c, err = cli.Parse(native.Args); err != nil {
			return argError(err)
		}
		hideErrors = native.HideErrors
	}
	if code, done := runJob(c, w); done {
		return code
	}
	opts, err := cli.Apply(c)
	if err != nil {
		return argErrorOrGlob(err)
	}
	opts.Output.HideErrors = hideErrors
	noteLateJobWord(c, opts)
	if code, done := startBackground(opts, args, w); done {
		return code
	}
	restore, err := openLog(opts)
	if err != nil {
		return argError(err)
	}
	defer restore()
	if c.Job == cli.JobInspect {
		opts.Output.Snoop = true
	}
	return transfer(ctx, opts, do, stream, w)
}

// openHeaderFile opens the --save-headers file once for the command, as
// curl -D does: every response of every URL goes into it. "-" is stdout.
func openHeaderFile(opts *config.Options, w *os.File) (int, bool) {
	switch opts.Output.SaveHeaders {
	case "":
		return 0, true
	case "-":
		opts.Output.HeaderOut = w
		return 0, true
	}
	// The header file is not the body, so --output-overwrite does not
	// keep it: curl -D replaces it.
	out := opts.Output
	out.NoOverwrite = false
	f, err := config.OpenOutput(out, opts.Output.SaveHeaders, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--save-headers: %v\n", err)
		return config.WriteFileFailed.Int(), false
	}
	opts.Output.HeaderOut = f
	return 0, true
}

// openTraceFile opens the --trace file once for the command, as curl
// does. "-" is stderr, so the trace does not mix with the body.
func openTraceFile(opts *config.Options) (int, bool) {
	switch opts.Output.Trace {
	case "":
		return 0, true
	case "-":
		opts.Output.TraceOut = os.Stderr
		return 0, true
	}
	f, err := os.Create(opts.Output.Trace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--trace: %v\n", err)
		return config.WriteFileFailed.Int(), false
	}
	opts.Output.TraceOut = f
	return 0, true
}

// openWARC opens the --warc file once for the command. The stdout path
// reads the body outside the transport, where the WARC records are
// written, so --warc needs a file: -o, ghola download or ghola mirror.
func openWARC(opts *config.Options) (int, bool) {
	if opts.Output.WARC == "" {
		return 0, true
	}
	if !opts.Mirror.On && !config.ShouldStream(opts) && opts.Output.Dir == "" && !opts.Output.RemoteName {
		return argError(fmt.Errorf("--warc records the transfers to a file: use -o, ghola download or ghola mirror")), false
	}
	f, err := os.Create(opts.Output.WARC)
	if err != nil {
		fmt.Fprintf(os.Stderr, "--warc: %v\n", err)
		return config.WriteFileFailed.Int(), false
	}
	opts.Output.WARCOut = f
	return 0, true
}

// compatCommands translate the syntax of curl, wget and aria2c into native
// arguments (v1 spec, section 6).
var compatCommands = map[string]func([]string) (*cli.Compat, error){
	cli.JobCurl: cli.FromCurl, cli.JobWget: cli.FromWget, cli.JobAria2c: cli.FromAria2c,
}

// argError reports an argument error. A bad URL glob exits 3, as curl's
// "URL using bad/illegal format".
func argErrorOrGlob(err error) int {
	var ge *glob.Error
	if errors.As(err, &ge) {
		reportArgError(os.Stderr, &config.UsageError{Err: err, Usage: cli.ShortUsage(config.Version)}, useColor(os.Stderr))
		return config.URLMalformat.Int()
	}
	return argError(err)
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
	case cli.JobFetch, cli.JobDownload, cli.JobInspect, cli.JobMirror:
		return 0, false
	case cli.JobJobs:
		return runJobsCommand(c.Args, w), true
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
	case cli.JobCompletion:
		if len(c.Args) != 1 {
			return argError(fmt.Errorf("ghola completion takes one shell: bash, zsh, fish or powershell")), true
		}
		text, err := cli.Completion(c.Args[0])
		if err != nil {
			return argError(err), true
		}
		fmt.Fprint(w, text)
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

// transfer opens the --save-headers file for the whole command, and runs
// the transfers.
func transfer(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {
	if code, ok := openHeaderFile(opts, w); !ok {
		return code
	}
	if f, ok := opts.Output.HeaderOut.(*os.File); ok && f != w {
		defer f.Close()
	}
	if code, ok := openTraceFile(opts); !ok {
		return code
	}
	if f, ok := opts.Output.TraceOut.(*os.File); ok && f != os.Stderr {
		defer f.Close()
	}
	if v := opts.Output.RateTotalValue; v != "" {
		bps, err := config.ParseRate(v) // Apply checked it
		if err != nil {
			return argError(err)
		}
		opts.Output.RateTotal = client.NewSharedLimiter(bps) // one limit for every transfer
	}
	if code, ok := openWARC(opts); !ok {
		return code
	}
	if f, ok := opts.Output.WARCOut.(*os.File); ok {
		defer f.Close()
	}
	// Parallel --jobs share these files: each block is one locked write.
	opts.Output.HeaderOut = locked(opts.Output.HeaderOut)
	opts.Output.TraceOut = locked(opts.Output.TraceOut)
	opts.Output.WARCOut = locked(opts.Output.WARCOut)
	return transferAll(ctx, opts, do, stream, w)
}

// lockedWriter makes each write whole when several jobs share a writer.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// locked returns w behind a lock, or nil for nil.
func locked(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	return &lockedWriter{w: w}
}

// readBodySources reads the request body from its source: --upload, -d -
// (stdin), -d @file, --form, --data-binary @file or --field. A source that
// cannot be read exits 26.
func readBodySources(opts *config.Options) error {
	if opts.Transfer != "" {
		b, err := os.ReadFile(opts.Transfer)
		if err != nil {
			return fmt.Errorf("read upload file: %w", err)
		}
		opts.Data = string(b)
	}
	if err := readDataArg(opts); err != nil {
		return err
	}
	return readFormOrBinary(opts)
}

// readDataArg reads -d: - is stdin, @file is the file, and
// --data-literal takes the value as it is.
func readDataArg(opts *config.Options) error {
	// Stdin/file substitution: -d - reads stdin, -d @file reads from file.
	if opts.DataLiteral {
		// --data-literal: the value is the body, also when it starts with @.
	} else if opts.Data == "-" || opts.Data == "@-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		opts.Data = string(b)
	} else if len(opts.Data) > 1 && opts.Data[0] == '@' {
		b, err := os.ReadFile(opts.Data[1:])
		if err != nil {
			return fmt.Errorf("read data file: %w", err)
		}
		opts.Data = string(b)
	}
	return nil
}

// readFormOrBinary builds the body of --form, --data-binary or --field.
func readFormOrBinary(opts *config.Options) error {
	if len(opts.Form) > 0 {
		ct, body, err := config.BuildFormBody(opts.Form)
		if err != nil {
			return fmt.Errorf("form: %w", err)
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
				return fmt.Errorf("read --data-binary file: %w", err)
			}
			data = string(b)
		}
		opts.Data = data
	} else if len(opts.DataURLEncode) > 0 {
		opts.Data = config.URLEncodeData(opts.DataURLEncode)
	}
	return nil
}

// transferAll reads the request body sources and fetches each URL.
func transferAll(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {
	if err := readBodySources(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return config.ReadFileFailed.Int()
	}

	// Several URLs go one after the other, as in curl (audit finding M9).
	// A failure does not stop the run, and the exit code is the last
	// failure's, as in curl (v1 defaults, item 10B, decided by Jon on
	// 2026-11-18).
	if opts.Mirror.On {
		return runMirror(ctx, opts, do, stream)
	}
	runs := make([]config.Options, len(opts.URLs))
	for i, u := range opts.URLs {
		runs[i] = *opts
		runs[i].URL = u
		runs[i].CredentialURL = opts.URL // -u and --auth-bearer belong to the first URL's server
		runs[i].UseNetrc()               // netrc: the entry of this URL's own host
		if len(opts.Output.PerURL) == len(opts.URLs) {
			runs[i].Output.SetTarget(opts.Output.PerURL[i]) // one -o for each URL, in order
		}
		if i < len(opts.Output.InputArgs) {
			if err := cli.ApplyInputArgs(&runs[i], opts.Output.InputArgs[i]); err != nil {
				return argError(err) // Apply parsed them; a setter may still refuse a value
			}
		}
		if runs[i].Output.RemoteName && len(opts.URLs) > 1 {
			config.TakeRemoteName(&runs[i]) // each URL under its own name
		}
	}
	codes, err := runAll(ctx, runs, do, stream, w)
	if err != nil {
		return argError(err)
	}
	last := config.NoError.Int()
	for _, code := range codes { // the last failure in URL order, whatever the timing
		if code != config.NoError.Int() {
			last = code
		}
	}
	return last
}

// jobsBoard returns the board that shows one line for each job, when
// stderr is a terminal (or --progress forces it) and the jobs fit on it;
// else nil, and the meters of the jobs stay off.
func jobsBoard(runs []config.Options) *client.Board {
	o := runs[0].Output
	if o.Progress == "off" || o.Silent || len(runs) > client.MaxBoardLines {
		return nil
	}
	if o.Progress != "on" && !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	return client.NewBoard(os.Stderr, len(runs))
}

// jobName is the short name of a job on the board: its file, else its URL.
func jobName(o config.Options) string {
	if o.Output.File != "" {
		return filepath.Base(o.Output.File)
	}
	return o.URL
}

// templateMu keeps the --output-template lines of parallel jobs apart.
var templateMu sync.Mutex

// runAll runs the transfers, up to --jobs at the same time, and returns
// each exit code in URL order. Bodies on stdout would mix, so several
// jobs need a file for each URL. The progress meters of several jobs
// would share one line, so they are off unless --progress forces them.
func runAll(ctx context.Context, runs []config.Options, do client.Doer, stream client.Streamer, w *os.File) ([]int, error) {
	codes := make([]int, len(runs))
	jobs := max(1, min(runs[0].Jobs, len(runs)))
	if jobs > 1 {
		board := jobsBoard(runs)
		for i := range runs {
			if runs[i].Output.File == "" && !runs[i].Output.RemoteName {
				return nil, fmt.Errorf("--jobs %d writes several bodies at once, so it needs a file for each URL: use -o DIR/, one -o for each URL, or ghola download", runs[0].Jobs)
			}
			switch {
			case board != nil:
				runs[i].Output.MeterOut = board.Line(i, jobName(runs[i]))
			case runs[i].Output.Progress == "":
				runs[i].Output.Progress = "off"
			}
		}
	}
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i := range runs {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			codes[i] = fetchWithTemplate(ctx, &runs[i], do, stream, w)
		}()
	}
	wg.Wait()
	return codes, nil
}

// fetchWithTemplate runs fetch, and then writes --output-template for
// the transfer, also after a failure, as curl -w does.
func fetchWithTemplate(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {
	if opts.Output.Template == nil && opts.Output.Events == nil {
		return fetch(ctx, opts, do, stream, w)
	}
	info := &config.TransferInfo{URL: opts.URL}
	opts.Output.Info = info
	opts.Output.Events.Start(opts.URL)
	start := time.Now()
	code := fetch(ctx, opts, do, stream, w)
	info.Time, info.ExitCode = time.Since(start), code
	if code == config.NoError.Int() {
		info.File = opts.Output.File
	}
	opts.Output.Events.End(*info)
	if opts.Output.Template == nil {
		return code
	}
	templateMu.Lock()
	defer templateMu.Unlock()
	if err := opts.Output.Template.Write(w, os.Stderr, *info); err != nil {
		fmt.Fprintf(os.Stderr, "--output-template: %v\n", err)
		return config.WriteFileFailed.Int()
	}
	return code
}

// failed reports err, records it for %{errormsg}, and returns its exit
// code.
func failed(opts *config.Options, err error) int {
	if info := opts.Output.Info; info != nil {
		info.ErrorMsg = err.Error()
	}
	if !opts.Output.HideErrors {
		fmt.Fprintf(os.Stderr, "Failed: %s\n", err)
	}
	return exitFor(err).Int()
}

// fetch sends the request for opts.URL and writes the response. It returns
// the exit code. --max-time covers each URL on its own, as in curl.
func fetch(ctx context.Context, opts *config.Options, do client.Doer, stream client.Streamer, w *os.File) int {
	ctx, cancel := client.WithTimeout(ctx, opts)
	defer cancel()

	if config.ShouldStream(opts) {
		first := recordJob(opts, "") // a download that is stopped stays in the list
		if err := client.Download(ctx, opts, stream); err != nil {
			code := failed(opts, err)
			if errors.Is(err, config.ErrOutputExists) {
				forgetJob(opts, first) // the file is the user's, not a partial download
			} else {
				recordJob(opts, first) // the remote name may have changed the file
			}
			runHook(opts.Output.OnError, opts)
			return code
		}
		forgetJob(opts, first)
		runHook(opts.Output.OnComplete, opts)
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
		return failed(opts, err)
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
		if info := opts.Output.Info; info != nil {
			info.ErrorMsg = err.Error()
		}
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

// noteLateJobWord writes a note when a job word comes after an option: the
// job word must be the first argument, so "ghola -v download URL" fetches
// http://download/ and URL. The note tells the user why.
func noteLateJobWord(c *cli.Command, opts *config.Options) {
	if opts.Output.Silent {
		return
	}
	for _, a := range c.Args {
		if cli.IsJob(a) {
			fmt.Fprintf(os.Stderr, "Note: %q is read as a URL; a job word must be the first argument: ghola %s [options] URL...\n", a, a)
		}
	}
}
