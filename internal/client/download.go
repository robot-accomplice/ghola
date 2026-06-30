// Package client — download.go implements the streaming download path used
// for file-destined GET requests. It writes bodies directly to disk with
// bounded memory, in contrast to the buffered FetchURL path.
package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// Download fetches opts.URL and streams it to opts.Output.File with bounded
// memory. Routing (when to call this vs. FetchURL) is decided by
// config.ShouldStream in the CLI entry point.
func Download(ctx context.Context, opts *config.Options, stream Streamer) error {
	if stream == nil {
		stream = DefaultStreamer
	}
	finalURL, err := resolveTarget(ctx, opts, stream)
	if err != nil {
		return err
	}
	return downloadSingle(ctx, opts, stream, finalURL)
}

// resolveTarget follows redirects with a HEAD request and returns the final
// URL. It does not require the server to support HEAD beyond returning a
// status; the body (if any) is discarded.
func resolveTarget(ctx context.Context, opts *config.Options, stream Streamer) (string, error) {
	target := opts.URL
	for hops := 0; hops <= opts.Resilience.MaxRedirs; hops++ {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, target, fasthttp.MethodHead, "")
		meta, err := stream(ctx, opts, req, io.Discard)
		fasthttp.ReleaseRequest(req)
		if err != nil {
			return "", err
		}
		if !isRedirectStatus(meta.StatusCode) {
			return target, nil
		}
		// HEAD redirect: re-fetch with GET semantics handled in downloadSingle.
		// Location is not in StreamMeta; fall through to a GET-based resolve.
		return resolveTargetViaGet(ctx, opts, stream, target)
	}
	return target, nil
}

// resolveTargetViaGet resolves redirects when HEAD returns 3xx by issuing a
// ranged GET (bytes=0-0) and reading Location from the response.
func resolveTargetViaGet(ctx context.Context, opts *config.Options, stream Streamer, start string) (string, error) {
	target := start
	for hops := 0; hops <= opts.Resilience.MaxRedirs; hops++ {
		req := fasthttp.AcquireRequest()
		buildDownloadRequest(req, opts, target, fasthttp.MethodGet, "")
		req.Header.Set("Range", "bytes=0-0")
		loc, status, err := streamLocation(ctx, opts, stream, req)
		fasthttp.ReleaseRequest(req)
		if err != nil {
			return "", err
		}
		if !isRedirectStatus(status) {
			return target, nil
		}
		resolved, ok := resolveLocation(target, loc)
		if !ok {
			return "", fmt.Errorf("download: bad redirect Location %q", loc)
		}
		target = resolved
	}
	return "", fmt.Errorf("download: too many redirects (>%d)", opts.Resilience.MaxRedirs)
}

// streamLocation runs a request, discards the body, and returns the Location
// header + status. It needs the raw header, so it uses a buffered transport
// call via DefaultDoer-style access is avoided; instead it relies on the
// Streamer plus a header sink. Since StreamMeta omits Location, we capture it
// through a one-shot buffered fetch.
func streamLocation(ctx context.Context, opts *config.Options, stream Streamer, req *fasthttp.Request) (string, int, error) {
	// Use FetchURL's transport directly for redirect resolution: a HEAD/probe
	// body is tiny, so a buffered fetch is acceptable here.
	rsp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(rsp)
	tr, err := gholatransport.New(opts, "")
	if err != nil {
		return "", 0, err
	}
	if err := tr.Do(ctx, req, rsp); err != nil {
		return "", 0, err
	}
	return string(rsp.Header.Peek("Location")), rsp.Header.StatusCode(), nil
}

func downloadSingle(ctx context.Context, opts *config.Options, stream Streamer, url string) error {
	f, err := os.OpenFile(opts.Output.File, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	buildDownloadRequest(req, opts, url, fasthttp.MethodGet, opts.Data)

	meta, err := stream(ctx, opts, req, f)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if meta.StatusCode < 200 || meta.StatusCode >= 300 {
		return fmt.Errorf("download: non-2xx status %d", meta.StatusCode)
	}
	return nil
}

// buildDownloadRequest sets URI, method, body, headers, and the active
// browser profile / ghost / auth identity, mirroring prepareRequest but for
// the streaming path. It reuses the profile builder for header realism.
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
	overrides := parseHeaderOverrides(opts.Headers)
	for _, generated := range profile.BuildHeaders(active, profile.HeaderOptions{
		Method:         method,
		TargetURL:      url,
		RefererMode:    opts.Stealth.Referer,
		ExplicitUA:     resolveUserAgent(opts, active),
		ExplicitLang:   opts.Stealth.AcceptLanguage,
		StealthHeaders: opts.Stealth.StealthHeaders,
	}) {
		if _, ok := overrides[strings.ToLower(generated.Key)]; ok {
			continue
		}
		req.Header.Set(generated.Key, generated.Value)
	}
	for _, h := range opts.Headers {
		if k, v, ok := parseHeader(h); ok {
			req.Header.Set(k, v)
		}
	}
	if _, ok := overrides["user-agent"]; !ok {
		req.Header.Set("User-Agent", resolveUserAgent(opts, active))
	}
	if opts.Stealth.Ghost {
		applyGhostSign(req, url)
	}
}
