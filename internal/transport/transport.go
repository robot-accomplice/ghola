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
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	fhttptrace "github.com/bogdanfinn/fhttp/httptrace"
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
	Location      string // Location, "" when absent
	RetryAfter    string // Retry-After, "" when absent
	ETag          string // ETag, "" when absent
	// ContentEncoding is the Content-Encoding header: the codings that
	// --compressed removes. "" when absent.
	ContentEncoding string
}

// NewStreamMeta builds the StreamMeta of a response. Both stacks call it
// with their own header lookup (net/http and fhttp have different Header
// types), so a new field reaches both. Test fakes call it too, so they
// cannot drift from the real stacks.
func NewStreamMeta(status int, length int64, header func(string) string) StreamMeta {
	return StreamMeta{
		StatusCode:      status,
		ContentLength:   length, // -1 when unknown, per net/http
		AcceptRanges:    strings.EqualFold(header("Accept-Ranges"), "bytes"),
		LastModified:    header("Last-Modified"),
		Disposition:     header("Content-Disposition"),
		ContentRange:    header("Content-Range"),
		Location:        header("Location"),
		RetryAfter:      header("Retry-After"),
		ETag:            header("ETag"),
		ContentEncoding: header("Content-Encoding"),
	}
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
	limits     limits
}

type tlsClientTransport struct {
	client   tlsclient.HttpClient
	name     string
	limits   limits
	envProxy bool // no --proxy: take the proxy from the environment per request
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
		Proxy:                  func(r *http.Request) (*url.URL, error) { return proxyFromEnv(r.URL) },
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
		limits:     newLimits(opts.Resilience),
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
		client:   client,
		name:     "tls-client",
		limits:   newLimits(opts.Resilience),
		envProxy: selectedProxy == "",
	}, nil
}

func (t *simpleTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	resp, w, err := t.send(ctx, req)
	if err != nil {
		return err
	}
	fillResponse(rsp, resp.StatusCode, resp.Header, w.body(resp.Body), resp.ContentLength)
	return nil
}

// send sends req under the curl limits (--connect-timeout, --speed-limit).
// The caller must pass resp.Body through w.body.
func (t *simpleTransport) send(ctx context.Context, req *fasthttp.Request) (*http.Response, *watch, error) {
	ctx, w := t.limits.start(ctx)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { w.gotConn() }})
	httpReq, err := newHTTPRequest(ctx, req)
	if err != nil {
		return nil, nil, w.fail(err)
	}
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, w.fail(err)
	}
	return resp, w, nil
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
	noEngineUserAgent(httpReq.Header)
	return httpReq, nil
}

// noEngineUserAgent stops net/http and fhttp from sending their own
// User-Agent (Go-http-client) when the request has none. ghola sets a
// User-Agent on every request, so a missing one was removed with
// -H "User-Agent:" (audit finding M7). With the header present and empty,
// net/http and fhttp over HTTP/2 send no User-Agent. fhttp over HTTP/1
// sends an empty "User-Agent:" line: no value, so no library name. An empty
// slice would remove that line too, but fhttp's HTTP/2 encoder reads
// Values[0] and would panic.
func noEngineUserAgent[H ~map[string][]string](h H) {
	if _, ok := h["User-Agent"]; !ok {
		h["User-Agent"] = []string{""}
	}
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
	resp, w, err := t.send(ctx, req)
	if err != nil {
		return StreamMeta{}, err
	}
	body := w.body(resp.Body)
	defer body.Close()

	meta := NewStreamMeta(resp.StatusCode, resp.ContentLength, resp.Header.Get)
	return meta, CopyBody(sink, meta, body)
}

func (t *tlsClientTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpResp, w, err := t.send(ctx, req, orderedHeaders)
	if err != nil {
		return err
	}
	fillResponse(rsp, httpResp.StatusCode, httpResp.Header, w.body(httpResp.Body), httpResp.ContentLength)
	return nil
}

// orderedHeaders copies req's headers to httpReq in req's order, so the
// header order matches the browser profile.
func orderedHeaders(httpReq *fhttp.Request, req *fasthttp.Request) {
	headerOrder := make([]string, 0, 16)
	httpReq.Header = make(fhttp.Header)
	for key, value := range req.Header.All() {
		headerName := string(key)
		httpReq.Header.Set(headerName, string(value))
		headerOrder = append(headerOrder, strings.ToLower(headerName))
	}
	noEngineUserAgent(httpReq.Header)
	if len(headerOrder) > 0 {
		httpReq.Header[fhttp.HeaderOrderKey] = headerOrder
		httpReq.Header[fhttp.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	}
}

// plainHeaders copies req's headers to httpReq without an order.
func plainHeaders(httpReq *fhttp.Request, req *fasthttp.Request) {
	httpReq.Header = make(fhttp.Header)
	for key, value := range req.Header.All() {
		httpReq.Header.Set(string(key), string(value))
	}
	noEngineUserAgent(httpReq.Header)
}

// send sends req under the curl limits (--connect-timeout, --speed-limit).
// The caller must pass httpResp.Body through w.body.
func (t *tlsClientTransport) send(ctx context.Context, req *fasthttp.Request, setHeaders func(*fhttp.Request, *fasthttp.Request)) (*fhttp.Response, *watch, error) {
	ctx, w := t.limits.start(ctx)
	ctx = fhttptrace.WithClientTrace(ctx, &fhttptrace.ClientTrace{GotConn: func(fhttptrace.GotConnInfo) { w.gotConn() }})
	httpReq, err := fhttp.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), bytes.NewReader(req.Body()))
	if err != nil {
		return nil, nil, w.fail(err)
	}
	setHeaders(httpReq, req)
	if err := t.useEnvProxy(httpReq.URL); err != nil {
		return nil, nil, w.fail(err)
	}
	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, nil, w.fail(err)
	}
	return httpResp, w, nil
}

// useEnvProxy sets the client's proxy from the environment for target, as
// net/http does per request. tls-client reads no proxy variables itself.
func (t *tlsClientTransport) useEnvProxy(target *url.URL) error {
	if !t.envProxy {
		return nil
	}
	p, err := proxyFromEnv(target)
	if err != nil {
		return err
	}
	want := ""
	if p != nil {
		want = p.String()
	}
	if want == t.client.GetProxy() {
		return nil
	}
	return t.client.SetProxy(want)
}

func (t *tlsClientTransport) Name() string {
	return t.name
}

func (t *tlsClientTransport) Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error) {
	httpResp, w, err := t.send(ctx, req, plainHeaders)
	if err != nil {
		return StreamMeta{}, err
	}
	body := w.body(httpResp.Body)
	defer body.Close()

	meta := NewStreamMeta(httpResp.StatusCode, httpResp.ContentLength, httpResp.Header.Get)
	return meta, CopyBody(sink, meta, body)
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
