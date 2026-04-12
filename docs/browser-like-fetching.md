# Browser-Like Fetching for Ghola

## Purpose

Ghola should remain a command-line URL fetching tool, but it should stop advertising itself as a trivially synthetic client when users need to probe an endpoint that behaves differently for obvious bots.

This design adds browser-like behavior at the HTTP transport and request-shape layers without turning Ghola into a browser automation or scraping framework.

## Attribution

The direction for this work was informed by the ideas demonstrated in the Scrapling project by Karim Shoair. In particular, Scrapling highlighted the practical value of browser-like transport fingerprints, coherent headers, cookie continuity, and profile-aware sessions for non-automation fetch workflows.

Ghola intentionally adapts only the fetcher-oriented concepts that fit a command-line URL tool. It does not aim to reproduce Scrapling's scraping, browser automation, selector, or challenge-handling features.

## Goals

- Keep Ghola a single-purpose CLI fetcher and bridge sidecar.
- Improve request realism through coherent browser profiles.
- Add cookie persistence, proxy selection, and session continuity.
- Leave room for future TLS-level impersonation backends behind a transport abstraction.

## Non-Goals

- No DOM parsing or selector API.
- No browser automation or Playwright/CDP integration.
- No captcha solving or challenge-solving workflow.
- No crawl queues, spiders, or extraction pipelines.

## User-Facing Additions

- `--impersonate <profile>` picks a browser profile such as `chrome`, `chrome-123`, `firefox`, `safari`, or `edge`.
- `--stealth-headers` emits coherent browser-style headers that match the active profile.
- `--cookie-jar <file>` persists cookies between runs.
- `--cookie <name=value>` seeds the request and in-memory jar.
- `--proxy <url>` and `--proxy-file <path>` route requests through a proxy.
- `--proxy-auth <user:pass>` injects proxy credentials when needed.
- `--proxy-strategy <random|round-robin|sticky>` chooses from a proxy file.
- `--referer <auto|none|url>` controls referer generation.
- `--accept-language <value>` overrides the profile default.
- `--profile-list` prints the available browser profiles.

## Architecture

### `internal/profile`

Defines browser profiles and generates coherent headers. The initial implementation focuses on request headers, not TLS-level impersonation.

### `internal/cookies`

Implements a persistent JSON-backed cookie jar with basic domain, path, expiry, and secure matching.

### `internal/proxy`

Parses proxy inputs, loads proxy files, and selects a proxy according to the configured strategy.

### `internal/transport`

Provides the transport abstraction. The current implementation uses a `fasthttp` client with optional proxy dialing and leaves a clean seam for future impersonating transports.

### `internal/client`

Coordinates profile resolution, header generation, cookie persistence, redirects, retries, and transport use.

## Current Implementation Notes

- Browser-like header generation is implemented now.
- Cookie jar persistence is implemented now.
- Proxy selection is implemented now.
- The transport abstraction is implemented now, but true TLS/browser impersonation is still a follow-up backend task.
- `--http3` is parsed but not yet implemented by the default `fasthttp` transport.

## Follow-Up Work

- Add a TLS/browser impersonation backend behind `internal/transport`.
- Expose negotiated HTTP version and richer transport metadata in verbose mode.
- Improve sticky proxy and concurrent session behavior with a shared runtime session object.
- Carry explicit attribution for the original inspiration into any user-facing docs or release notes that describe this feature family.
