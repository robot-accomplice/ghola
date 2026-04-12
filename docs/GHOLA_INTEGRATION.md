# Ghola Integration Guide

Ghola is a Go-based surgical HTTP scout built for blockchain forensics.
Scope uses ghola as an optional sidecar to add stealth capabilities
(temporal drift, ghost signing, browser-like request shaping, and snoop mode) to its HTTP requests without
embedding Go code in the Rust binary.

Attribution: Ghola's browser-like fetching direction was inspired by ideas demonstrated in the Scrapling project by Karim Shoair. Ghola applies those ideas to a narrower command-line fetcher and sidecar bridge.

## Architecture

```text
┌───────────┐  JSON/HTTP   ┌──────────────────┐  fasthttp   ┌──────────┐
│   scope   │─────────────▶│  ghola --serve   │────────────▶│  target  │
│  (Rust)   │  :18789      │  bridge server   │             │  RPC     │
└───────────┘              └──────────────────┘             └──────────┘
      │                            │
      │ fallback (native reqwest)  │ drift / ghost / profile / retry
      └────────────────────────────┘
```

The two binaries communicate over a local JSON-over-HTTP bridge.
Scope never links against Go code; the bridge is standard HTTP on
`127.0.0.1:18789`.

## Installation

### 1. Install ghola

Download a prebuilt binary from the
[releases page](https://github.com/robot-accomplice/ghola/releases)
or build from source:

```bash
cd ghola/
go build -o ghola ./cmd/ghola
sudo mv ghola /usr/local/bin/
```

Verify it's in your PATH:

```bash
ghola --help
```

### 2. Enable the sidecar in scope

Create or edit `config.yaml` in the scope working directory
(or `~/.config/scope/config.yaml` for user-global config):

```yaml
use_ghola_sidecar: true
```

### 3. Verify integration

```bash
cd scope/
cargo run -- setup
```

You should see:

```text
--- [Scope Environment Check] ---

  ✓ ghola binary found in PATH
  ✓ use_ghola_sidecar = true

--- [Setup Complete] ---
```

## Sidecar Lifecycle

When `use_ghola_sidecar: true`, scope automatically manages the sidecar:

1. **Check** — scope probes `127.0.0.1:18789/health` via TCP connect.
2. **Spawn** — if unreachable, scope runs `ghola --serve` as a background
   child process.
3. **Wait** — polls the port for up to 5 seconds.
4. **Fallback** — if the bridge still isn't ready, scope falls back to
   native `reqwest` and prints a warning.

You can also start ghola manually:

```bash
ghola --serve
# ghola bridge listening on 127.0.0.1:18789
```

## Stealth Mode

Pass `--stealth` to scope to enable drift jitter and ghost signing:

```bash
cargo run -- --stealth https://mainnet.infura.io/v3/YOUR_KEY
```

This sends `drift: true` and `ghost: true` in the bridge payload, causing
ghola to:

- **Drift** — inject a cryptographically random delay (≤ 500 ms) before
  each request, breaking timing correlation.
- **Ghost sign** — attach a unique `X-Ghola-Identity` SHA-256 header,
  enabling request provenance without exposing real credentials.

When the bridge payload includes impersonation fields, ghola can also:

- **Impersonate** — use a pure-Go browser profile such as Chrome or Firefox.
- **Shape headers** — emit browser-like headers that match the active profile.
- **Persist cookies** — carry session cookies across requests with a cookie jar.
- **Use proxy pools** — route requests through a selected proxy strategy.

## Bridge Protocol

The bridge accepts `POST /` with a JSON body:

```json
{
  "url": "https://...",
  "method": "POST",
  "headers": {"Content-Type": "application/json"},
  "body": "{\"jsonrpc\":\"2.0\",\"method\":\"eth_blockNumber\",\"params\":[],\"id\":1}",
  "drift": true,
  "ghost": true,
  "retries": 2,
  "impersonate": "chrome",
  "stealth_headers": true,
  "cookie_jar": "/tmp/ghola-cookies.json",
  "proxy": "http://proxy.local:8080"
}
```

And returns:

```json
{
  "status_code": 200,
  "headers": {"Content-Type": "application/json"},
  "body": "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":\"0x10d4f1\"}"
}
```

Health check: `GET /health` → `{"status":"ok"}`

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `⚠️ Ghola not found` | Install ghola and ensure it's in `$PATH` |
| Sidecar timeout | Run `ghola --serve` manually and check stderr |
| Fallback to native HTTP | Check `config.yaml` has `use_ghola_sidecar: true` |
| Port 18789 in use | Kill existing ghola process: `lsof -ti:18789 \| xargs kill` |
