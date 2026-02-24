# Ghola

[![CI](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml/badge.svg)](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/robot-accomplice/ghola)](https://goreportcard.com/report/github.com/robot-accomplice/ghola)
[![GoDoc](https://pkg.go.dev/badge/github.com/robot-accomplice/ghola)](https://pkg.go.dev/github.com/robot-accomplice/ghola)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Ghola** is a high-performance, Go-based HTTP client designed as a tactical scout for blockchain forensic analysis and stealthy data acquisition. Built on [fasthttp](https://github.com/valyala/fasthttp), it compiles to a single zero-dependency binary.

## Features

### Tactical Stealth

| Flag | Description |
| ------ | ------------- |
| `-D, --drift <ms>` | **Temporal Drift** -- injects cryptographically random jitter into request timing to evade bot-detection and timing analysis. |
| `-G, --ghost` | **Ghost Sign** -- adds a unique `X-Ghola-Identity` header (SHA256 hash of timestamp + URL) for distributed auditing and traceability. |

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
| `-f` | **Fail silently** on non-2xx HTTP status. |

## Installation

### Quick Install (Recommended)

#### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/robot-accomplice/ghola/main/scripts/install.sh | sh
```

#### Windows (PowerShell)

```powershell
iwr https://raw.githubusercontent.com/robot-accomplice/ghola/main/scripts/install.ps1 -UseBasicParsing | iex
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

Pre-built binaries for Linux, macOS, and Windows are available on the [Releases](https://github.com/robot-accomplice/ghola/releases) page.

### Build from Source

```bash
git clone https://github.com/robot-accomplice/ghola.git
cd ghola
go build -o ghola ./cmd/ghola
```

## Usage

```bash
# Basic GET request
ghola https://httpbin.org/get

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

Ghola is structured as three internal packages wired together by a thin CLI entrypoint:

```text
cmd/ghola/          Entrypoint (os.Exit, arg wiring)
internal/config/    CLI flag parsing, Options, validation
internal/client/    HTTP transport, retry, drift, ghost, concurrency
internal/output/    Response rendering, file I/O, snoop mode
```

See [docs/architecture/](docs/architecture/) for C4 diagrams and a detailed dataflow walkthrough.

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
