# Ghola

[![CI](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml/badge.svg)](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/robot-accomplice/ghola)](https://goreportcard.com/report/github.com/robot-accomplice/ghola)
[![GoDoc](https://pkg.go.dev/badge/github.com/robot-accomplice/ghola)](https://pkg.go.dev/github.com/robot-accomplice/ghola)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Ghola** is a high-performance, Go-based HTTP client designed as a tactical scout for blockchain forensic analysis and browser-like URL fetching. It remains a command-line fetcher, not a scraping framework, and now includes a pure-Go browser impersonation path for transport- and session-level realism.

Attribution: the direction for Ghola's browser-like fetching work was inspired by ideas demonstrated in the [Scrapling](https://github.com/D4Vinci/Scrapling) project by Karim Shoair. Ghola adapts those ideas to a narrower fetcher-only workflow and does not implement Scrapling's scraping or automation features.

## Features

### Browser-Like Fetching

| Flag | Description |
| ------ | ------------- |
| `-D, --drift <ms>` | **Temporal Drift** -- injects cryptographically random jitter into request timing to evade bot-detection and timing analysis. |
| `-G, --ghost` | **Ghost Sign** -- adds a unique `X-Ghola-Identity` header (SHA256 hash of timestamp + URL) for distributed auditing and traceability. |
| `--impersonate <profile>` | **Pure-Go browser impersonation** -- use a browser-like transport profile such as `chrome`, `firefox`, `safari`, or `edge`. |
| `--stealth-headers` | **Coherent headers** -- generate browser-like headers that match the active profile. |
| `--http3` | **HTTP/3** -- enable HTTP/3 when supported by the active impersonation profile. |
| `--referer <mode>` | **Referer control** -- use `none`, `auto`, or an explicit URL. |
| `--accept-language <value>` | **Locale shaping** -- override the profile default `Accept-Language`. |

### Forensic & Companion Tools

| Flag | Description |
| ------ | ------------- |
| `-S, --snoop` | **Snoop Mode** -- pre-flight reconnaissance of target headers, security posture, and WAF detection (Cloudflare, X-Ray). |
| `-c, --chain <type>` | **Chain Shortcuts** -- pre-fills RPC headers for `eth`, `base`, or `solana` ecosystems. |
| `-r, --retry <n>` | **Autonomous Retries** -- exponential backoff with configurable base delay (`-b`). |

### Core Utility

| Flag | Description |
| ------ | ------------- |
| `-n <int>` | **Concurrent connections** via goroutines (first successful response wins). |
| `-w, --wget` | **Wget mode** -- auto-saves to local file using the inferred remote filename. |
| `-u <user:pass>` | **Basic Auth** -- native HTTP basic authentication. |
| `-T <file>` | **Upload** -- send a local file as the request body. |
| `-o <file>` | **Output** -- write response body to a file. |
| `-H <header>` | **Custom headers** -- repeatable. Default: `Content-Type: application/json`. |
| `-X <method>` | **HTTP method** -- defaults to GET, auto-switches to POST when `-d` is used. |
| `-i` | **Include headers** in output. |
| `-v` | **Verbose** -- show ghost signature and extra diagnostics. |
| `-s` | **Silent** -- suppress all output. |
| `-f` | **Fail** on non-2xx HTTP status (exit non-zero, suppress body output). |
| `-L, --location` | **Follow redirects** (HTTP 3xx). |
| `--max-redirs <n>` | **Max redirects** to follow when `--location` is enabled. |
| `--retry-http` | **Retry on statuses** (e.g. 429, 5xx) in addition to transport failures. |
| `--timeout <ms>` | **Request timeout** in milliseconds (0 disables). |
| `--cookie-jar <file>` | **Persistent cookies** -- save and reuse cookies across runs. |
| `--cookie <name=value>` | **Seed cookies** -- add one or more cookies to the request session. |
| `--proxy <url>` | **Single proxy** -- route traffic through an HTTP proxy. |
| `--proxy-file <path>` | **Proxy pool** -- load proxies from a newline-delimited file. |
| `--proxy-strategy <mode>` | **Proxy selection** -- choose from `sticky`, `random`, or `round-robin`. |
| `--profile-list` | **List profiles** -- print the built-in impersonation profiles and exit. |

## Installation

### Quick Install (Recommended)

#### macOS / Linux

```bash
bash <(curl -fsSL https://roboticus.ai/ghola-install.sh)
```

#### Windows (PowerShell)

```powershell
irm https://roboticus.ai/ghola-install.ps1 | iex
```

After install, verify with:

```bash
ghola --version
```

### Go Install

```bash
go install github.com/robot-accomplice/ghola/cmd/ghola@latest
```

### Download Binary

Pre-built binaries for Linux, macOS, and Windows are available on the [Releases](https://github.com/robot-accomplice/ghola/releases) page and at [roboticus.ai](https://roboticus.ai).

### Build from Source

```bash
git clone https://github.com/robot-accomplice/ghola.git
cd ghola
go build -o ghola ./cmd/ghola
```

Ghola now targets Go 1.26 for development and release builds.

## Usage

```bash
# Basic GET request
ghola https://httpbin.org/get

# Browser-like fetch with a pure-Go Chrome fingerprint
ghola --impersonate chrome --stealth-headers https://example.com

# Stateful probing with persistent cookies
ghola --impersonate firefox --cookie-jar ~/.config/ghola/cookies.json https://example.com

# Proxy-backed browser-like fetch
ghola --impersonate chrome --proxy http://user:pass@proxy.local:8080 https://example.com

# Inspect available impersonation profiles
ghola --profile-list

# POST with data and verbose ghost signing
ghola -vG -d '{"query": "balance"}' https://rpc.example.com

# Snoop mode -- check security posture
ghola -S https://example.com

# Chain-aware request with retries
ghola -c base -r 3 https://rpc.base.org

# Download a file (wget mode)
ghola -w https://example.com/report.pdf

# Concurrent connections with drift jitter
ghola -n 5 -D 200 https://api.example.com/data
```

## Architecture

Ghola is structured as a CLI entrypoint plus focused internal packages:

```text
cmd/ghola/          Entrypoint (os.Exit, arg wiring)
internal/config/    CLI flag parsing, Options, validation
internal/client/    Request pipeline (fetchState, retry, drift, redirect, cookie/session)
internal/transport/ Runtime backend selection (simple or pure-Go impersonation)
internal/profile/   Browser-like profile definitions and header generation
internal/cookies/   Persistent JSON cookie jar
internal/proxy/     Proxy parsing and selection
internal/output/    Response rendering, file I/O, snoop mode
```

See [docs/architecture/](docs/architecture/) for C4 diagrams and a detailed dataflow walkthrough.
See [docs/browser-like-fetching.md](docs/browser-like-fetching.md) and [docs/pure-go-impersonation.md](docs/pure-go-impersonation.md) for the browser-like transport design.

## Testing

```bash
go test -v -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

Coverage is enforced in CI with two gates:
- Each package must be at least **80%**
- Coverage must **not regress** below recorded baselines

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and PR guidelines.

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) -- Copyright (c) 2026 Jonathan Machen
