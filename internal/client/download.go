// Package client — download.go implements the streaming download path used
// for file-destined GET requests. It writes bodies directly to disk with
// bounded memory, in contrast to the buffered FetchURL path.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
	"golang.org/x/term"
)

// meterOut returns where the progress meter goes: stderr when it is a
// terminal, else nil. A log file or a pipe gets no meter lines (audit
// finding M4). Tests replace it.
var meterOut = func() io.Writer {
	if term.IsTerminal(int(os.Stderr.Fd())) {
		return os.Stderr
	}
	return nil
}

// byteRange is an inclusive [start, end] byte range for a Range request.
type byteRange struct {
	start int64
	end   int64
}

// splitRanges divides size bytes into at most `parts` contiguous inclusive
// ranges. When parts exceeds size, it clamps to one range per byte. May return
// fewer than `parts` segments if bytes are exhausted before all iterations.
// Returns nil for size <= 0.
func splitRanges(size int64, parts int) []byteRange {
	if size <= 0 || parts <= 0 {
		return nil
	}
	if int64(parts) > size {
		parts = int(size)
	}
	// Ceiling division makes earlier segments larger; loop breaks when start >= size.
	chunk := (size + int64(parts) - 1) / int64(parts)
	ranges := make([]byteRange, 0, parts)
	var start int64
	for i := 0; i < parts; i++ {
		end := start + chunk - 1
		if end >= size {
			end = size - 1
		}
		ranges = append(ranges, byteRange{start: start, end: end})
		start = end + 1
		if start >= size {
			break
		}
	}
	return ranges
}

// Download fetches opts.URL and streams it to opts.Output.File with bounded
// memory. Routing (when to call this vs. FetchURL) is decided by
// config.ShouldStream in the CLI entry point. When concurrency > 1 and the
// server supports Range requests, the download is split into parallel segments.
func Download(ctx context.Context, opts *config.Options, stream Streamer) error {
	if stream == nil {
		stream = DefaultStreamer
	}

	// Build shared limiter (aggregate cap across all segments).
	var limiter *rateLimiter
	if opts.Output.LimitRate != "" {
		bps, perr := config.ParseRate(opts.Output.LimitRate)
		if perr != nil {
			return fmt.Errorf("parse --limit-rate: %w", perr)
		}
		limiter = newRateLimiter(bps)
	}

	// Build shared progress writer (suppressed under --silent, and when
	// stderr is not a terminal). The total comes from the response.
	var progress *progressWriter
	if out := meterOut(); out != nil && !opts.Output.Silent {
		progress = newProgressWriter(io.Discard, out, -1)
	}

	meta, err := fetchToFile(ctx, opts, stream, limiter, progress)
	if err != nil {
		return err
	}

	if progress != nil {
		progress.done()
	}
	if opts.Output.RemoteTime && meta.LastModified != "" {
		if ts, perr := time.Parse(time.RFC1123, meta.LastModified); perr == nil {
			_ = os.Chtimes(opts.Output.File, ts, ts)
		}
	}
	return nil
}

// fetchToFile downloads opts.URL to the file: as parallel ranges when it can,
// else as one GET. No path sends HEAD (audit finding M5). Only a download
// that can be split probes the URL first, with a 1-byte ranged GET.
func fetchToFile(ctx context.Context, opts *config.Options, stream Streamer, limiter *rateLimiter, progress *progressWriter) (gholatransport.StreamMeta, error) {
	if !mightSegment(opts) {
		return downloadSingle(ctx, opts, stream, opts.URL, limiter, progress)
	}
	url, meta, err := probeRanges(ctx, opts, stream)
	if err != nil {
		return meta, err
	}
	if canSegment(opts, meta) {
		applyRemoteName(opts, meta)
		if progress != nil {
			progress.setTotal(meta.ContentLength)
		}
		err := downloadSegmented(ctx, opts, stream, url, meta.ContentLength, limiter, progress)
		if !errors.Is(err, errRangeIgnored) {
			return meta, err
		}
		// The server advertised ranges but sent the full body. No byte
		// was written; fetch the file as one stream instead.
	}
	return downloadSingle(ctx, opts, stream, url, limiter, progress)
}

// errRedirect stops a request at a redirect answer, before a body byte is
// written. followRedirects then sends the next request.
var errRedirect = errors.New("redirect")

// errProbed stops a probe after the headers. The probe needs no body.
var errProbed = errors.New("probe done")

// followRedirects calls get for url, and again for each redirect that get
// reports with errRedirect, up to --max-redirs hops. It returns the last URL
// and its meta. A download follows redirects without -L, as it did before
// the HEAD request was removed.
func followRedirects(opts *config.Options, url string, get func(string) (gholatransport.StreamMeta, error)) (string, gholatransport.StreamMeta, error) {
	for hops := 0; ; hops++ {
		meta, err := get(url)
		if !errors.Is(err, errRedirect) {
			return url, meta, err
		}
		if hops >= opts.Resilience.MaxRedirs {
			return url, meta, fmt.Errorf("download: %w", tooManyRedirects(opts))
		}
		next, ok := resolveLocation(url, meta.Location)
		if !ok {
			return url, meta, fmt.Errorf("download: bad redirect Location %q", meta.Location)
		}
		url = next
	}
}

// isRedirect reports a redirect answer with a Location to follow.
func isRedirect(meta gholatransport.StreamMeta) bool {
	return isRedirectStatus(meta.StatusCode) && meta.Location != ""
}

// probeRanges follows redirects with 1-byte ranged GETs and returns the final
// URL and its meta. A 206 answer proves Range support, and its Content-Range
// gives the size. The probe stops after the headers, so a server that
// ignores Range does not send it the whole file.
func probeRanges(ctx context.Context, opts *config.Options, stream Streamer) (string, gholatransport.StreamMeta, error) {
	return followRedirects(opts, opts.URL, func(url string) (gholatransport.StreamMeta, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		buildDownloadRequest(req, opts, url, fasthttp.MethodGet, "")
		req.Header.Set("Range", "bytes=0-0")
		meta, err := stream(ctx, opts, req, newCheckedSink(io.Discard, func(m gholatransport.StreamMeta) error {
			if isRedirect(m) {
				return errRedirect
			}
			return errProbed
		}))
		if errors.Is(err, errRedirect) {
			return meta, err
		}
		if err != nil && !errors.Is(err, errProbed) {
			return meta, fmt.Errorf("probe: %w", err)
		}
		return rangeMeta(meta), nil
	})
}

// rangeMeta returns the probe's meta with the full size and the Range
// support that a 206 answer shows.
func rangeMeta(meta gholatransport.StreamMeta) gholatransport.StreamMeta {
	meta.AcceptRanges = false
	if meta.StatusCode != fasthttp.StatusPartialContent {
		return meta
	}
	if total := contentRangeTotal(meta.ContentRange); total >= 0 {
		meta.ContentLength = total
		meta.AcceptRanges = true
	}
	return meta
}

// applyRemoteName sets the output file from Content-Disposition under -J.
func applyRemoteName(opts *config.Options, meta gholatransport.StreamMeta) {
	if !opts.Output.RemoteHeader {
		return
	}
	if name := dispositionFilename(meta.Disposition); name != "" {
		opts.Output.File = name
	}
}

// canSegment reports whether a segmented (parallel Range) download applies.
// Segments are range GETs without a body (config.ShouldStream sends only
// GET here), so a GET with a body (-X GET -d) goes as one request.
func canSegment(opts *config.Options, meta gholatransport.StreamMeta) bool {
	return mightSegment(opts) &&
		meta.AcceptRanges &&
		meta.ContentLength >= config.DefaultSegmentMinBytes
}

// mightSegment reports whether the options allow a segmented download. Only
// then is the URL probed first.
func mightSegment(opts *config.Options) bool {
	return opts.Concurrency > 1 &&
		opts.Data == "" &&
		opts.Output.File != "" &&
		!opts.Stealth.Compressed && // wired in Task 9; field exists by then
		opts.Output.ContinueAt == "" // wired in Task 5; resume forces single-stream
}

// downloadSegmented fetches the file as parallel ranges. On any error it
// removes the output file, so that a preallocated file of the full size
// never looks like a finished download.
func downloadSegmented(ctx context.Context, opts *config.Options, stream Streamer, url string, size int64, limiter *rateLimiter, progress *progressWriter) (err error) {
	f, err := os.OpenFile(opts.Output.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(opts.Output.File)
		}
	}()
	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("preallocate output file: %w", err)
	}

	ranges := splitRanges(size, opts.Concurrency)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(ranges))
	var wg sync.WaitGroup
	for _, r := range ranges {
		wg.Add(1)
		go func(r byteRange) {
			defer wg.Done()
			if err := fetchSegment(ctx, opts, stream, url, f, r, limiter, progress); err != nil {
				cancel()
				errs <- err
				return
			}
			errs <- nil
		}(r)
	}
	wg.Wait()
	close(errs)

	// Report the first real cause. A segment that stopped only because another
	// segment failed returns context.Canceled, which hides the cause.
	var first error
	for e := range errs {
		if e == nil {
			continue
		}
		if first == nil || (errors.Is(first, context.Canceled) && !errors.Is(e, context.Canceled)) {
			first = e
		}
	}
	if first != nil {
		return fmt.Errorf("download segment: %w", first)
	}
	return nil
}

// fetchSegment fetches one byte range and writes it at its offset in f.
func fetchSegment(ctx context.Context, opts *config.Options, stream Streamer, url string, f *os.File, r byteRange, limiter *rateLimiter, progress *progressWriter) error {
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, "")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.start, r.end))

	// Sink nesting (innermost first): file offset writer → rate limit → progress.
	// Each segment uses its own offset writer; limiter and progress are shared
	// across goroutines (both are mutex-guarded). The segment progress tap
	// forwards bytes to its local sink while counting into the shared progress.
	var w io.Writer = io.NewOffsetWriter(f, r.start)
	if limiter != nil {
		w = newRateLimitedWriter(w, limiter)
	}
	if progress != nil {
		// Wrap with a per-segment tap that forwards to this segment's sink and
		// also counts into the aggregate progress (mutex-guarded in Write).
		w = newProgressTap(w, progress)
	}
	// Outermost: no byte reaches the file unless the server sent exactly r.
	_, err := stream(ctx, opts, req, newCheckedSink(w, expectRange(r.start, r.end)))
	return err
}

func downloadSingle(ctx context.Context, opts *config.Options, stream Streamer, url string, limiter *rateLimiter, progress *progressWriter) (gholatransport.StreamMeta, error) {
	plan := singleGet{flags: os.O_CREATE | os.O_WRONLY, limiter: limiter, progress: progress}

	switch {
	case opts.Output.ContinueAt != "":
		off, err := resumeOffset(opts)
		if err != nil {
			return gholatransport.StreamMeta{}, err
		}
		plan.offset = off
		plan.flags |= os.O_APPEND
		plan.rangeHeader = fmt.Sprintf("bytes=%d-", off)
	case opts.Output.Range != "":
		plan.rangeHeader = "bytes=" + strings.TrimPrefix(opts.Output.Range, "bytes=")
		plan.flags |= os.O_TRUNC
	default:
		plan.flags |= os.O_TRUNC
	}

	_, meta, err := followRedirects(opts, url, func(url string) (gholatransport.StreamMeta, error) {
		return plan.getWithRetry(ctx, opts, stream, url)
	})
	return meta, err
}

// errRetryStatus refuses a response with a retryable status under
// --retry-http while retries remain, so its body is not written.
var errRetryStatus = errors.New("retryable HTTP status")

// getWithRetry sends the GET for url up to 1 + --retry times, as FetchURL
// does. It retries a request that failed before it had a response, and,
// with --retry-http, a retryable status. It does not retry after the file
// opened: a body that broke off is not written twice.
func (g singleGet) getWithRetry(ctx context.Context, opts *config.Options, stream Streamer, url string) (gholatransport.StreamMeta, error) {
	for attempt := 0; ; attempt++ {
		g.retryStatus = opts.Resilience.RetryHTTP && attempt < opts.Resilience.Retries
		meta, opened, err := g.get(ctx, opts, stream, url)
		retry := errors.Is(err, errRetryStatus) || (err != nil && meta.StatusCode == 0 && !opened)
		if !retry || attempt >= opts.Resilience.Retries {
			return meta, err
		}
		wait := backoffWait(opts, attempt+1)
		if d, ok := retryAfter(meta.RetryAfter); ok {
			wait = d
		}
		if werr := waitToRetry(ctx, opts, attempt+1, wait); werr != nil {
			return meta, werr
		}
	}
}

// singleGet is one single-stream download: how to open the file, the Range
// to ask for, and the shared limiter and meter.
type singleGet struct {
	flags       int
	offset      int64
	rangeHeader string
	limiter     *rateLimiter
	progress    *progressWriter
	retryStatus bool // refuse a retryable status: a retry follows
}

// get sends one GET for url and writes an accepted body to the file. A
// redirect answer gives errRedirect and writes nothing. opened reports that
// the file opened.
func (g singleGet) get(ctx context.Context, opts *config.Options, stream Streamer, url string) (meta gholatransport.StreamMeta, opened bool, err error) {
	f := &lazyFile{name: opts.Output.File, flags: g.flags}
	defer f.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, opts.Data)
	if g.rangeHeader != "" {
		req.Header.Set("Range", g.rangeHeader)
	}

	// Sink nesting (innermost first): file -> rate limit -> progress.
	// progress is outermost so it counts bytes after throttling (bytes on disk).
	// When --compressed, gzipSink wraps the whole chain so that compressed bytes
	// received from the network are decoded before reaching the file (and rate
	// limiting / progress count plain bytes on disk).
	var sink io.Writer = f
	if g.limiter != nil {
		sink = newRateLimitedWriter(sink, g.limiter)
	}
	if g.progress != nil {
		g.progress.dst = sink
		sink = g.progress
	}

	var flush func() error
	if opts.Stealth.Compressed {
		var gw io.Writer
		gw, flush = gzipSink(sink)
		sink = gw
	}

	meta, err = stream(ctx, opts, req, newCheckedSink(sink, g.check(opts, f)))
	if flush != nil {
		if ferr := flush(); ferr != nil && err == nil {
			err = ferr
		}
	}
	opened = f.f != nil
	switch {
	case errors.Is(err, errRangeIgnored):
		return meta, opened, fmt.Errorf("download: server ignored resume Range (status 200); cannot continue at %d", g.offset)
	case errors.Is(err, errRedirect):
		return meta, opened, err
	case err != nil:
		return meta, opened, fmt.Errorf("download: %w", err)
	case meta.StatusCode < 200 || meta.StatusCode >= 300:
		return meta, opened, fmt.Errorf("download: non-2xx status %d", meta.StatusCode)
	}
	return meta, opened, nil
}

// check returns the header check of one GET. The file opens only for an
// accepted response. -f refuses an HTTP error (audit finding H7), and a
// resume appends only a 206 that starts at the offset. A refused response
// writes no file and leaves an existing file unchanged.
func (g singleGet) check(opts *config.Options, f *lazyFile) func(gholatransport.StreamMeta) error {
	return func(meta gholatransport.StreamMeta) error {
		if isRedirect(meta) {
			return errRedirect
		}
		if g.retryStatus && isRetryableHTTPStatus(meta.StatusCode) {
			return fmt.Errorf("%w %d", errRetryStatus, meta.StatusCode)
		}
		if opts.Output.FailsOn(meta.StatusCode) {
			return fmt.Errorf("%w: %d", config.ErrHTTPStatus, meta.StatusCode)
		}
		if g.offset > 0 {
			if err := expectRange(g.offset, -1)(meta); err != nil {
				return err
			}
		}
		applyRemoteName(opts, meta)
		f.name = opts.Output.File
		if g.progress != nil {
			g.progress.setTotal(meterTotal(opts, meta))
		}
		return f.open()
	}
}

// meterTotal returns the meter's total for the accepted response, or -1 when
// it is not known. The meter counts the bytes written to the file. With
// --compressed they are decoded bytes, and Content-Length counts the
// compressed bytes, so the decoded total is not known.
func meterTotal(opts *config.Options, meta gholatransport.StreamMeta) int64 {
	if opts.Stealth.Compressed {
		return -1
	}
	return meta.ContentLength
}

// lazyFile is the output file of a single-stream download. It opens only
// when the response is accepted, so a refused response creates no file and
// does not truncate an existing one.
type lazyFile struct {
	name  string
	flags int
	f     *os.File
}

func (l *lazyFile) open() error {
	f, err := os.OpenFile(l.name, l.flags, 0644)
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	l.f = f
	return nil
}

// Write is called only after open: checkedSink refuses a write before the
// check passes.
func (l *lazyFile) Write(p []byte) (int, error) { return l.f.Write(p) }

func (l *lazyFile) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

// resumeOffset computes the resume byte offset: an explicit value, or the
// current file size when --continue-at is "-".
func resumeOffset(opts *config.Options) (int64, error) {
	if opts.Output.ContinueAt != "-" {
		return strconv.ParseInt(opts.Output.ContinueAt, 10, 64)
	}
	info, err := os.Stat(opts.Output.File)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // nothing downloaded yet; start from the beginning
		}
		return 0, fmt.Errorf("stat for resume: %w", err)
	}
	return info.Size(), nil
}

// dispositionFilename extracts a safe basename from a Content-Disposition
// header value, or "" if none. Any directory components are stripped to
// prevent path traversal.
func dispositionFilename(disposition string) string {
	const marker = "filename="
	i := strings.Index(strings.ToLower(disposition), marker)
	if i < 0 {
		return ""
	}
	name := disposition[i+len(marker):]
	if j := strings.IndexByte(name, ';'); j >= 0 {
		name = name[:j]
	}
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"`)
	name = path.Base(name) // strip any directory components
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return name
}

// gzipSink returns a writer that transparently gunzips data written to it
// before passing it to dst, plus a flush func to call after streaming
// completes. It sniffs the gzip magic bytes (0x1f 0x8b): if the stream is NOT
// gzip (e.g. the server ignored Accept-Encoding), bytes pass through
// undecoded, so a non-compressing server never corrupts the output.
func gzipSink(dst io.Writer) (io.Writer, func() error) {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		decoded, err := gholatransport.DecodeGzip(pr)
		if err != nil {
			pr.CloseWithError(err)
			done <- err
			return
		}
		_, cerr := io.Copy(dst, decoded)
		done <- cerr
	}()
	return pw, func() error {
		pw.Close()
		return <-done
	}
}

// buildDownloadRequest sets URI, method, body and headers for the streaming
// path. The headers come from setHeaders, as on the stdout path.
func buildDownloadRequest(req *fasthttp.Request, opts *config.Options, url, method, data string) {
	req.SetRequestURI(url)
	req.Header.SetMethod(method)
	if data != "" {
		req.SetBodyString(data)
	}

	var active profile.BrowserProfile
	if opts.Stealth.Impersonate != "" || opts.Stealth.StealthHeaders {
		if resolved, err := profile.Resolve(opts.Stealth.Impersonate); err == nil {
			active = resolved
		}
	}
	setHeaders(req, opts, active, parseHeaderOverrides(opts.Headers), url, method)
}
