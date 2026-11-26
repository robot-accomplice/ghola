# Ghola Integration Guide

Ghola is a Go-based surgical HTTP scout built for blockchain forensics.
Scope uses ghola as an optional sidecar to add stealth capabilities
(temporal drift, ghost signing, browser-like request shaping, and snoop mode) to its HTTP requests without
embedding Go code in the Rust binary.

Attribution: Ghola's browser-like fetching direction was inspired by ideas demonstrated in the Scrapling project by Karim Shoair. Ghola applies those ideas to a narrower command-line fetcher and sidecar bridge.

## Architecture

```text
┌───────────┐  JSON/HTTP   ┌──────────────────┐  fasthttp   ┌──────────┐
│   scope   │─────────────▶│   ghola serve    │────────────▶│  target  │
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

1. **Check** — scope sends `GET /health` to `127.0.0.1:18789`.
2. **Spawn** — if there is no answer, scope runs `ghola serve` as a
   background child process.
3. **Wait** — polls `/health` for up to 5 seconds.
4. **Token** — scope reads the bridge token (see [Bridge Security](#bridge-security)).
5. **Fallback** — if the bridge is not ready or the token cannot be read,
   scope falls back to native `reqwest` and prints a warning.

You can also start ghola manually:

```bash
ghola serve
# ghola bridge listening on 127.0.0.1:18789 (token in <config dir>/ghola/bridge-token)
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

## Bridge Security

The bridge sends real requests from your machine, so it accepts commands
only from a local client of the same user. Each request to `POST /` and to
`/jsonrpc` must pass three checks:

1. The `Host` header is `127.0.0.1:18789` or `localhost:18789`. This stops
   DNS rebinding.
2. The `Authorization` header is `Bearer <token>`. At each start, the bridge
   writes a new random token to `bridge-token` in the user config directory
   (`os.UserConfigDir()/ghola`): `%AppData%\ghola` on Windows,
   `~/Library/Application Support/ghola` on macOS, and
   `$XDG_CONFIG_HOME/ghola` or `~/.config/ghola` on Linux. The file has mode
   `0600`. It is written after the port is bound, so a second bridge that
   cannot bind never replaces the token of the bridge that runs.
3. The `Content-Type` is `application/json`.

A web page cannot read the token, and a browser sends a cross-origin
`application/json` POST only after a CORS preflight, which the bridge
refuses. `GET /health` needs only the `Host` check.

The bridge logs each refused request to stderr, with the reason.

## Bridge Protocol

The bridge accepts `POST /` with a JSON body and the headers above:

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
  "cookie_jar": "cookies.txt",
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

### Request fields

Only `url` is required. The `url` must be `http://` or `https://`.

| Field | Type | Meaning |
| --- | --- | --- |
| `url` | string | The URL to fetch. |
| `method` | string | The HTTP method. The default is `GET`. |
| `headers` | object | Request headers, name to value. |
| `body` | string | The request body. |
| `drift` | bool | Add a random wait before the request. |
| `ghost` | bool | Accepted and ignored. v0 sent an `X-Ghola-Identity` header for it. |
| `retries` | int | Retries after a transient failure. |
| `buffer_size` | int | Raises the response header limit (bytes). |
| `impersonate` | string | A browser profile: `ghola profiles` lists them. |
| `stealth_headers` | bool | Send the browser's default headers in its order. |
| `cookie_jar` | string | A cookie jar file to read and write back. It must be a path below the folder where `ghola serve` runs. A file that is not a cookie jar is never replaced. |
| `cookies` | string list | Cookies to send, as `name=value`. |
| `proxy` | string | A proxy URL. |
| `proxy_auth` | string | The proxy credentials, `user:password`. |
| `proxy_file` | string | A file with one proxy URL on each line. |
| `proxy_strategy` | string | `sticky`, `random` or `round-robin` for `proxy_file`. |
| `referer` | string | The Referer header. |
| `accept_language` | string | Overrides the profile's Accept-Language. |
| `http3` | bool | Use HTTP/3 with an impersonation profile that has it. |
| `timeout` | int | The time limit of the request, in milliseconds. 0 means no limit. |
| `location` | bool | Follow redirects. The bridge follows redirects to `http://` and `https://` only. |
| `max_redirs` | int | The redirect limit. |
| `retry_http` | bool | Also retry the HTTP status codes that ghola retries. |
| `user` | string | Credentials, `user:password`. |

### Errors

Every error reply has the same four fields as a normal reply, so a client
can parse it with one type:

```json
{"status_code": 0, "headers": {}, "body": "", "error": "url is required"}
```

The bridge also uses this shape when it refuses a request before the
handler runs: a body larger than 4 MiB (HTTP 413), a request that does not
parse, or a client that is too slow.

### Limits

- A client has 30 s to send its request. The upstream fetch has no bridge
  limit: use `timeout` for it.
- An idle keep-alive connection closes after 2 min.
- The request body may be 4 MiB at most.

## JSON-RPC endpoint

`/jsonrpc` takes JSON-RPC 2.0 over HTTP POST and over WebSocket, with the
same three checks as `POST /`. A WebSocket upgrade must also pass the
Origin check. It manages download jobs, as aria2's RPC does.

| Method | Params | Result |
| --- | --- | --- |
| `ghola.add` | `{"urls": [...], "args": [...]}` | The job IDs (`gid`), one for each URL. |
| `ghola.list` | none | The jobs: `gid`, `url`, `file`, `status`, `bytes` and `error`. |
| `ghola.pause` | `{"gid": "..."}` | The `gid`. Only an `active` or `paused` job. |
| `ghola.resume` | `{"gid": "..."}` | The `gid`. Only a `paused` job; it resumes with `-c`. |
| `ghola.stop` | `{"gid": "..."}` | The `gid`. Only an `active` or `paused` job; it becomes `removed`. |
| `ghola.purge` | none | The number of finished jobs that it removed. |

A job status is `active`, `paused`, `complete`, `error` or `removed`. Over
WebSocket, the server also sends the notifications `ghola.onComplete` and
`ghola.onError`, with params `[{"gid": "..."}]`. The answer to a request
always comes before the notification of its job.

`args` holds the options of `ghola download`, in long or short form. Only
these are allowed: `--output`, `--output-name`, `--connections`,
`--checksum`, `--header`, `--user-agent`, `--referer`, `--rate-max`,
`--retry-max`, `--retry-delay`, `--timeout`, `--timeout-connect`,
`--source`, `--follow-max`, `--impersonate`, `--max-size` and `--cookie`.
Rules:

1. `--output` must name a path below the folder where `ghola serve` runs.
   An existing file is never replaced.
2. `--source` and each URL must be `http://` or `https://`. Redirects go to
   `http://` and `https://` only.
3. Options that run a command or read a local file are refused.

JSON-RPC rules: one request at a time (a batch gets error -32600); a
notification (a request with no `id`) runs and gets no reply (HTTP 204);
an `id` must be a string, a number or null.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `⚠️ Ghola not found` | Install ghola and ensure it's in `$PATH` |
| Sidecar timeout | Run `ghola serve` manually and check stderr |
| Fallback to native HTTP | Check `config.yaml` has `use_ghola_sidecar: true` |
| Port 18789 in use | Kill existing ghola process: `lsof -ti:18789 \| xargs kill` |
| `401 missing or wrong bridge token` | Restart `ghola serve`; a bridge from before token support may still run |
