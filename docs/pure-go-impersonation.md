# Pure-Go TLS and Browser Fingerprint Impersonation

## Attribution

This design is inspired by ideas demonstrated in Scrapling by Karim Shoair, especially the value of making a fetcher look like a real browser at the network and session layers. Ghola is not attempting to become a scraping framework; it is applying a narrower subset of those ideas to a command-line URL fetcher.

## Purpose

Implement a true pure-Go impersonation backend for Ghola so that browser-like TLS, HTTP/2, and optional HTTP/3 fingerprints can be used without shelling out to an external binary.

## Decision

Use a Go-native impersonation backend built on `tls-client`, which itself is implemented in Go on top of `fhttp` and `uTLS`.

This gives Ghola:

- browser-profile TLS fingerprints
- browser-profile HTTP/2 behavior
- optional HTTP/3 support
- header ordering controls
- proxy support
- cookie jar support

without requiring an external executable.

## Scope

In scope:

- transport-level impersonation for `--impersonate`
- mapping Ghola browser profiles onto `tls-client` profiles
- proxy support via the impersonating backend
- optional HTTP/3 when the selected profile supports it
- preserving Ghola's retry, redirect, drift, and output behavior

Out of scope:

- DOM or selector APIs
- browser automation
- challenge solving
- scraping workflows

## Architecture

### `internal/profile`

Extends the browser profile model with a `TLSClientProfile` identifier so Ghola can map a user-facing profile such as `chrome` or `firefox-123` onto a concrete `tls-client` profile.

### `internal/transport`

Provides two backends:

- simple transport for plain requests
- `tlsClientTransport` for profile-backed impersonation

Selection rule:

- use `tlsClientTransport` when `--impersonate` is set
- use simple transport otherwise

### `internal/client`

Continues to own:

- retries
- redirect handling
- drift
- CLI header overrides
- Ghola cookie persistence

The transport backend owns:

- actual protocol negotiation
- browser fingerprint selection
- proxy dialing
- connection reuse within the transport

## Mapping Strategy

User-facing profile names should remain stable even if the backend profile changes.

Initial mapping:

- `chrome` -> latest stable Chrome profile available in `tls-client`
- `firefox` -> latest stable Firefox profile available in `tls-client`
- `safari` -> latest stable Safari macOS profile available in `tls-client`
- `edge` -> nearest Chrome-family profile until a dedicated Edge profile exists in the backend

Versioned names:

- `chrome-123` -> best available backend Chrome profile for that major version, or nearest compatible fallback
- `firefox-123` -> matching backend Firefox profile when available

## Transport Behavior

When impersonation is enabled:

- build a `tls-client` HTTP client with the mapped profile
- disable internal redirect following so Ghola preserves its own redirect logic
- disable HTTP/3 unless `--http3` is explicitly enabled
- pass proxy URL directly into `tls-client` when selected
- use request header ordering to match the generated/browser-like headers as closely as possible

## Verification

Primary verification targets:

- local unit tests for backend selection and conversion
- fingerprint endpoints for TLS/HTTP/2/HTTP/3 inspection
- regression tests for cookie persistence and proxy routing

## Risks

- backend profile availability may change across dependency upgrades
- Edge parity depends on backend support and may initially use a Chrome-family fallback
- HTTP/3 support can behave differently across environments, so it should remain opt-in

## Rollout

1. Add `tls-client` backend behind the transport abstraction.
2. Map Ghola profiles to backend profiles.
3. Keep the simple backend as fallback.
4. Expand tests and documentation.
