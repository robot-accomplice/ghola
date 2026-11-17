package transport

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	tlsprofiles "github.com/bogdanfinn/tls-client/profiles"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	"github.com/valyala/fasthttp"
)

// StreamMeta carries the response status line and the headers the download
// orchestrator needs, without materializing the body.
type StreamMeta struct {
	StatusCode    int
	ContentLength int64 // -1 when unknown
	AcceptRanges  bool
	LastModified  string
	Disposition   string // Content-Disposition
	ContentRange  string // Content-Range, "" when absent
}

// HeaderChecker is implemented by a sink that must see the response headers
// before it accepts the first body byte. A download sink uses it to refuse a
// response that does not match the requested byte range, so that no byte of
// a wrong response reaches the file.
type HeaderChecker interface {
	CheckHeaders(meta StreamMeta) error
}

// CopyBody runs sink's HeaderChecker (when sink has one) and then copies body
// to sink. When the check fails, CopyBody writes nothing and returns the
// check error. Every Stream implementation calls it.
func CopyBody(sink io.Writer, meta StreamMeta, body io.Reader) error {
	if c, ok := sink.(HeaderChecker); ok {
		if err := c.CheckHeaders(meta); err != nil {
			return err
		}
	}
	_, err := io.Copy(sink, body)
	return err
}

// Transport performs a request using the selected runtime backend.
type Transport interface {
	Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error
	// Stream sends req and copies the response body to sink without buffering
	// it in memory. It does NOT follow redirects; callers handle 3xx via the
	// returned StreamMeta. The caller owns sink.
	Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error)
	Name() string
}

// simpleTransport serves requests without impersonation. Do and Stream use
// the same net/http client, so an option reaches both or neither.
type simpleTransport struct {
	httpClient *http.Client
	name       string
}

type tlsClientTransport struct {
	client tlsclient.HttpClient
	name   string
}

// buildTLSConfig assembles a *tls.Config from the TLS-related options, or
// returns nil when none are set (use the transport default). -k disables
// verification; --cacert pins a custom CA; --cert/--key add a client cert.
func buildTLSConfig(opts *config.Options) (*tls.Config, error) {
	if !opts.Stealth.Insecure && opts.Stealth.CACert == "" && opts.Stealth.ClientCert == "" {
		return nil, nil
	}
	cfg := &tls.Config{InsecureSkipVerify: opts.Stealth.Insecure} //nolint:gosec // opt-in via -k
	if opts.Stealth.CACert != "" {
		pem, err := os.ReadFile(opts.Stealth.CACert)
		if err != nil {
			return nil, fmt.Errorf("read --cacert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--cacert %q contains no valid certificates", opts.Stealth.CACert)
		}
		cfg.RootCAs = pool
	}
	if opts.Stealth.ClientCert != "" {
		if opts.Stealth.ClientKey == "" {
			return nil, fmt.Errorf("--cert requires --key")
		}
		cert, err := tls.LoadX509KeyPair(opts.Stealth.ClientCert, opts.Stealth.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// New constructs the current transport backend.
func New(opts *config.Options, selectedProxy string) (Transport, error) {
	if strings.TrimSpace(opts.Stealth.Impersonate) != "" {
		return newTLSClientTransport(opts, selectedProxy)
	}
	if opts.Stealth.HTTP3 {
		return nil, fmt.Errorf("--http3 requires --impersonate with the pure-Go transport backend")
	}

	tlsCfg, err := buildTLSConfig(opts)
	if err != nil {
		return nil, err
	}

	// net/http streams resp.Body whatever the Content-Length. fasthttp's
	// client read a body with a known length into memory, which caused the
	// v0.6.0 large-download OOM, and it was removed for decision D3.
	httpTransport := &http.Transport{
		TLSClientConfig:   tlsCfg,
		ForceAttemptHTTP2: true,
		// Never auto-negotiate/transparently decompress: downloads must be
		// byte-exact, and --compressed is handled explicitly by the caller.
		DisableCompression:     true,
		Proxy:                  http.ProxyFromEnvironment,
		MaxResponseHeaderBytes: maxHeaderBytes(opts.Resilience.BufferSize),
	}
	if selectedProxy != "" {
		proxyURL, err := url.Parse(selectedProxy)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url: %w", err)
		}
		httpTransport.Proxy = http.ProxyURL(proxyURL)
	}
	httpClient := &http.Client{
		Transport: httpTransport,
		// Do NOT follow redirects; the download orchestrator inspects 3xx and
		// resolves the target itself (probe/resolveTarget).
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return &simpleTransport{
		httpClient: httpClient,
		name:       "net/http",
	}, nil
}

// maxHeaderBytes is the response header limit. --buffer-size can raise
// net/http's default limit (1 MiB) but not lower it. Its v0 default (4096)
// came from fasthttp's read buffer, which made larger headers fail.
func maxHeaderBytes(bufferSize int) int64 {
	if bufferSize > http.DefaultMaxHeaderBytes {
		return int64(bufferSize)
	}
	return http.DefaultMaxHeaderBytes
}

func newTLSClientTransport(opts *config.Options, selectedProxy string) (Transport, error) {
	activeProfile, err := profile.Resolve(opts.Stealth.Impersonate)
	if err != nil {
		return nil, err
	}
	if opts.Stealth.HTTP3 && !activeProfile.SupportsHTTP3 {
		return nil, fmt.Errorf("profile %q does not support http3 in the pure-Go backend", activeProfile.Name)
	}

	mappedProfile, ok := tlsprofiles.MappedTLSClients[activeProfile.TLSClientProfileName()]
	if !ok {
		return nil, fmt.Errorf("no tls-client profile mapping for %q", activeProfile.Name)
	}

	// Client certs and a custom CA are not supported on the impersonation
	// backend (tls-client exposes no client-cert/CA-pool API in the vendored
	// version); fail loud rather than silently ignoring them.
	if opts.Stealth.ClientCert != "" || opts.Stealth.CACert != "" {
		return nil, fmt.Errorf("--cert/--key/--cacert are not supported with --impersonate; use the default transport")
	}

	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(mappedProfile),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithRandomTLSExtensionOrder(),
	}
	if opts.Stealth.Insecure {
		options = append(options, tlsclient.WithInsecureSkipVerify())
	}
	if opts.Resilience.Timeout > 0 {
		options = append(options, tlsclient.WithTimeoutMilliseconds(opts.Resilience.Timeout))
	}
	if selectedProxy != "" {
		options = append(options, tlsclient.WithProxyUrl(selectedProxy))
	}
	if !opts.Stealth.HTTP3 {
		options = append(options, tlsclient.WithDisableHttp3())
	}

	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, err
	}

	return &tlsClientTransport{
		client: client,
		name:   "tls-client",
	}, nil
}

func (t *simpleTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpReq, err := newHTTPRequest(ctx, req)
	if err != nil {
		return err
	}
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	fillResponse(rsp, resp.StatusCode, resp.Header, resp.Body, resp.ContentLength)
	return nil
}

func (t *simpleTransport) Name() string {
	return t.name
}

// newHTTPRequest converts req to a net/http request.
func newHTTPRequest(ctx context.Context, req *fasthttp.Request) (*http.Request, error) {
	var body io.Reader
	if b := req.Body(); len(b) > 0 {
		body = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), body)
	if err != nil {
		return nil, err
	}
	for key, value := range req.Header.All() {
		name := string(key)
		val := string(value)
		// Host must be set on the request struct, not the header map; skip
		// Content-Length (net/http computes it from the body).
		switch {
		case strings.EqualFold(name, "Host"):
			httpReq.Host = val
		case strings.EqualFold(name, "Content-Length"):
			// let net/http set it
		default:
			httpReq.Header.Add(name, val)
		}
	}
	return httpReq, nil
}

// fillResponse copies a net/http or fhttp response into rsp. It does not
// read the body: rsp gets it as a body stream, so a large body never has to
// fit in memory (audit finding M2). fasthttp closes the stream when rsp is
// reset or released. contentLength is -1 when unknown.
//
// The stream is set before the headers: a Content-Length header from the
// server then replaces the length. tls-client reports -1 even when the
// server sent the header.
func fillResponse(rsp *fasthttp.Response, status int, header map[string][]string, body io.ReadCloser, contentLength int64) {
	rsp.SetStatusCode(status)
	rsp.SetBodyStream(body, int(contentLength))
	for key, values := range header {
		if key == fhttp.HeaderOrderKey || key == fhttp.PHeaderOrderKey {
			continue
		}
		for _, value := range values {
			rsp.Header.Add(key, value)
		}
	}
}

func (t *simpleTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	httpReq, err := newHTTPRequest(ctx, req)
	if err != nil {
		return StreamMeta{}, err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return StreamMeta{}, err
	}
	defer resp.Body.Close()

	meta := StreamMeta{
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength, // -1 when unknown, per net/http
		AcceptRanges:  strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes"),
		LastModified:  resp.Header.Get("Last-Modified"),
		Disposition:   resp.Header.Get("Content-Disposition"),
		ContentRange:  resp.Header.Get("Content-Range"),
	}
	return meta, CopyBody(sink, meta, resp.Body)
}

func (t *tlsClientTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpReq, err := fhttp.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), bytes.NewReader(req.Body()))
	if err != nil {
		return err
	}

	headerOrder := make([]string, 0, 16)
	httpReq.Header = make(fhttp.Header)
	for key, value := range req.Header.All() {
		headerName := string(key)
		httpReq.Header.Set(headerName, string(value))
		headerOrder = append(headerOrder, strings.ToLower(headerName))
	}
	if len(headerOrder) > 0 {
		httpReq.Header[fhttp.HeaderOrderKey] = headerOrder
		httpReq.Header[fhttp.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return err
	}
	fillResponse(rsp, httpResp.StatusCode, httpResp.Header, httpResp.Body, httpResp.ContentLength)
	return nil
}

func (t *tlsClientTransport) Name() string {
	return t.name
}

func (t *tlsClientTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	httpReq, err := fhttp.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), bytes.NewReader(req.Body()))
	if err != nil {
		return StreamMeta{}, err
	}
	httpReq.Header = make(fhttp.Header)
	for key, value := range req.Header.All() {
		httpReq.Header.Set(string(key), string(value))
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return StreamMeta{}, err
	}
	defer httpResp.Body.Close()

	meta := StreamMeta{
		StatusCode:    httpResp.StatusCode,
		ContentLength: httpResp.ContentLength, // -1 when unknown, per net/http
		AcceptRanges:  strings.EqualFold(httpResp.Header.Get("Accept-Ranges"), "bytes"),
		LastModified:  httpResp.Header.Get("Last-Modified"),
		Disposition:   httpResp.Header.Get("Content-Disposition"),
		ContentRange:  httpResp.Header.Get("Content-Range"),
	}
	return meta, CopyBody(sink, meta, httpResp.Body)
}

// ReadBody reads a streamed body into rsp's buffer and returns it. Use it,
// not rsp.Body(), when the whole body is needed: on a read error, Body()
// puts the error text in place of the body and reports nothing.
func ReadBody(rsp *fasthttp.Response) ([]byte, error) {
	stream := rsp.BodyStream()
	if stream == nil {
		return rsp.Body(), nil
	}
	b, err := io.ReadAll(stream)
	_ = rsp.CloseBodyStream()
	if err != nil {
		return nil, err
	}
	rsp.SetBody(b)
	return b, nil
}

// DecodeGzip returns a reader that decodes r when r starts with the gzip
// magic bytes (0x1f 0x8b). Otherwise it returns r's bytes unchanged, so a
// server that ignores Accept-Encoding never corrupts the output.
func DecodeGzip(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}
