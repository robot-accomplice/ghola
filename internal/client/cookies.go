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
