package client

import (
	"context"
	"fmt"
	"io"

	"github.com/robot-accomplice/ghola/internal/config"
	gholacookies "github.com/robot-accomplice/ghola/internal/cookies"
	gholatransport "github.com/robot-accomplice/ghola/internal/transport"
	"github.com/valyala/fasthttp"
)

// withInfo returns stream that records each response for
// --output-template: the status, the URL, the redirects and the body
// bytes. The probe writes no body, and the segments add up.
func withInfo(stream Streamer, opts *config.Options) Streamer {
	info := opts.Output.Info
	if info == nil {
		return stream
	}
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		meta, err := stream(ctx, opts, req, countWriter{sink, info})
		if meta.StatusCode != 0 {
			info.EffectiveURL, info.Status = string(req.URI().FullURI()), meta.StatusCode
			info.ContentType = meta.Header.Get("Content-Type")
			if meta.StatusCode >= 300 && meta.StatusCode < 400 && meta.Location != "" {
				info.Redirects++
			}
		}
		return meta, err
	}
}

type countWriter struct {
	w    io.Writer
	info *config.TransferInfo
}

// CheckHeaders passes the header check on to the sink, which the
// download path needs before the body (rangecheck.go).
func (c countWriter) CheckHeaders(meta gholatransport.StreamMeta) error {
	if hc, ok := c.w.(gholatransport.HeaderChecker); ok {
		return hc.CheckHeaders(meta)
	}
	return nil
}

func (c countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.info.AddSize(int64(n))
	return n, err
}

// withHeaderSave returns stream with --save-headers: the head of each
// response (the probe, each segment, each redirect) goes to HeaderOut.
func withHeaderSave(stream Streamer, opts *config.Options) Streamer {
	out := opts.Output.HeaderOut
	if out == nil {
		return stream
	}
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		meta, err := stream(ctx, opts, req, sink)
		if meta.StatusLine != "" {
			if _, werr := out.Write(meta.HeaderBlock()); werr != nil && err == nil {
				err = fmt.Errorf("--save-headers: %w", config.WriteError(werr))
			}
		}
		return meta, err
	}
}

// withCookieJar returns stream with the cookie jar on each request: the
// probe, every segment, and every redirect of a download. Before, only
// FetchURL (stdout) used the jar, so --cookie and --cookie-jar did nothing
// on -o. As in FetchURL, a Cookie header from -H wins over the jar, and the
// jar file is saved after each response.
func withCookieJar(stream Streamer, opts *config.Options) (Streamer, error) {
	jar, err := gholacookies.New(opts.Stealth.CookieJar)
	if err != nil {
		return nil, fmt.Errorf("load cookie jar: %w", err)
	}
	if err := jar.AddRequestCookies(opts.URL, opts.Stealth.Cookies); err != nil {
		return nil, err
	}
	_, overridden := parseHeaderOverrides(opts.Headers)["cookie"]
	return func(ctx context.Context, opts *config.Options, req *fasthttp.Request, sink io.Writer) (gholatransport.StreamMeta, error) {
		url := string(req.URI().FullURI())
		if v := jar.HeaderValue(url); v != "" && !overridden {
			req.Header.Set("Cookie", v)
		}
		meta, err := stream(ctx, opts, req, sink)
		jar.AbsorbSetCookies(url, meta.SetCookies)
		if serr := jar.Save(); serr != nil && err == nil {
			err = fmt.Errorf("save cookie jar: %w", config.WriteError(serr))
		}
		return meta, err
	}, nil
}
