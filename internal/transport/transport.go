package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	tlsprofiles "github.com/bogdanfinn/tls-client/profiles"
	"github.com/robot-accomplice/ghola/internal/config"
	"github.com/robot-accomplice/ghola/internal/profile"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

// Transport performs a request using the selected runtime backend.
type Transport interface {
	Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error
	Name() string
}

type simpleTransport struct {
	client *fasthttp.Client
	name   string
}

type tlsClientTransport struct {
	client tlsclient.HttpClient
	name   string
}

// New constructs the current transport backend.
func New(opts *config.Options, selectedProxy string) (Transport, error) {
	if strings.TrimSpace(opts.Stealth.Impersonate) != "" {
		return newTLSClientTransport(opts, selectedProxy)
	}
	if opts.Stealth.HTTP3 {
		return nil, fmt.Errorf("--http3 requires --impersonate with the pure-Go transport backend")
	}

	client := &fasthttp.Client{
		ReadBufferSize: opts.Resilience.BufferSize,
	}
	if selectedProxy != "" {
		proxyURL, err := url.Parse(selectedProxy)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url: %w", err)
		}
		client.Dial = fasthttpproxy.FasthttpHTTPDialer(proxyURL.String())
	}

	return &simpleTransport{
		client: client,
		name:   "fasthttp",
	}, nil
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

	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(mappedProfile),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithRandomTLSExtensionOrder(),
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
	if deadline, ok := ctx.Deadline(); ok {
		return t.client.DoDeadline(req, rsp, deadline)
	}
	return t.client.Do(req, rsp)
}

func (t *simpleTransport) Name() string {
	return t.name
}

func (t *tlsClientTransport) Do(ctx context.Context, req *fasthttp.Request, rsp *fasthttp.Response) error {
	httpReq, err := fhttp.NewRequestWithContext(ctx, string(req.Header.Method()), req.URI().String(), bytes.NewReader(req.Body()))
	if err != nil {
		return err
	}

	headerOrder := make([]string, 0, 16)
	httpReq.Header = make(fhttp.Header)
	req.Header.VisitAll(func(key, value []byte) {
		headerName := string(key)
		httpReq.Header.Set(headerName, string(value))
		headerOrder = append(headerOrder, strings.ToLower(headerName))
	})
	if len(headerOrder) > 0 {
		httpReq.Header[fhttp.HeaderOrderKey] = headerOrder
		httpReq.Header[fhttp.PHeaderOrderKey] = []string{":method", ":authority", ":scheme", ":path"}
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return err
	}

	rsp.SetStatusCode(httpResp.StatusCode)
	for key, values := range httpResp.Header {
		if key == fhttp.HeaderOrderKey || key == fhttp.PHeaderOrderKey {
			continue
		}
		for _, value := range values {
			rsp.Header.Add(key, value)
		}
	}
	rsp.SetBody(body)
	return nil
}

func (t *tlsClientTransport) Name() string {
	return t.name
}
