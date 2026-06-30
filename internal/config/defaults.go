// Package config defaults — single source of truth for all default values
// shared across the CLI entry point and the bridge sidecar.
package config

const (
	// DefaultMethod is the HTTP method when none is specified.
	DefaultMethod = "GET"
	// DefaultAgent is the User-Agent string used by ghola.
	DefaultAgent = "ghola"
	// DefaultBufferSize is the read buffer size for large headers.
	DefaultBufferSize = 4096
	// DefaultBackoffMs is the base backoff time for retries in milliseconds.
	DefaultBackoffMs = 1000
	// DefaultMaxRedirs is the maximum number of HTTP redirects to follow.
	DefaultMaxRedirs = 10
	// DefaultDriftMs is the temporal drift jitter when activated via bridge.
	DefaultDriftMs = 500
	// DefaultProxyStrategy is the proxy selection strategy.
	DefaultProxyStrategy = "sticky"
	// DefaultReferer is the default Referer header behavior.
	DefaultReferer = "none"
	// DefaultConcurrency is the default number of concurrent connections.
	DefaultConcurrency = 1
	// DefaultContentType is the default Content-Type header value.
	DefaultContentType = "Content-Type: application/json"
	// DefaultCopyBufferSize is the io.Copy chunk size for streaming downloads.
	DefaultCopyBufferSize = 256 * 1024
)
