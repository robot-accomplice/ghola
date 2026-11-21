<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/ghola-dark.svg">
  <img alt="" width="64" height="64" src="docs/assets/ghola-light.svg">
</picture>

# Ghola

[![CI](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml/badge.svg)](https://github.com/robot-accomplice/ghola/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/robot-accomplice/ghola)](https://goreportcard.com/report/github.com/robot-accomplice/ghola)
[![GoDoc](https://pkg.go.dev/badge/github.com/robot-accomplice/ghola)](https://pkg.go.dev/github.com/robot-accomplice/ghola)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Ghola** is a high-performance, Go-based HTTP client designed as a tactical scout for blockchain forensic analysis and browser-like URL fetching. It remains a command-line fetcher, not a scraping framework, and now includes a pure-Go browser impersonation path for transport- and session-level realism.

Attribution: the direction for Ghola's browser-like fetching work was inspired by ideas demonstrated in the [Scrapling](https://github.com/D4Vinci/Scrapling) project by Karim Shoair. Ghola adapts those ideas to a narrower fetcher-only workflow and does not implement Scrapling's scraping or automation features.

## Features

ghola v1 has one command line for three jobs: fetch (as curl), download (as aria2) and mirror (as wget). The full spec, with each option, its type, its default and its curl, wget and aria2 equivalents, is [docs/cli-v1.md](docs/cli-v1.md).

### Jobs

| Command | Job |
|---|---|
| `ghola URL...` | Fetch each URL. The body goes to stdout, or to `-o`. |
| `ghola download URL...` | Save each URL under its remote name; `-n N` splits a file into N ranges. |
| `ghola inspect URL` | Check the headers and the security posture (Cloudflare and other WAFs). |
| `ghola serve` | Run the local bridge on 127.0.0.1:18789 (for scope and scope-bca). Its JSON-RPC 2.0 endpoint `/jsonrpc` adds, lists, pauses, resumes and stops download jobs (`ghola.add`, `ghola.list`, `ghola.pause`, `ghola.resume`, `ghola.stop`, `ghola.purge`), behind the same token. It takes HTTP POST and WebSocket; a WebSocket client also gets `ghola.onComplete` and `ghola.onError`. |
| `ghola profiles` | List the browser profiles of `--impersonate`. |
| `ghola explain CODE` | Explain an exit code. |
| `ghola completion SHELL` | Write the completion script for bash, zsh, fish or PowerShell. |
| `ghola curl ARGS` | Run curl syntax, with curl's defaults. `--show-native` prints the ghola command. |
| `ghola wget ARGS`, `ghola aria2c ARGS` | Run wget or aria2c syntax, with that tool's defaults. |

### Short options

| Letter | Option | Letter | Option |
|---|---|---|---|
| `-o` | `--output FILE` (`-o -` is stdout, `-o DIR/` keeps the remote name) | `-c` | `--continue` (resume) |
| `-q` | `--quiet` (no meter, no notes; errors still show) | `-k` | `--no-tls-verify` |
| `-v` | `--verbose` | `-r` | `--retry-max N` |
| `-H` | `--header "Name: value"` | `-t` | `--timeout DURATION` |
| `-d` | `--data TEXT \| @file \| @-` | `-w` | `--output-template` |
| `-m` | `--method` | `-i` | `--input FILE` |
| `-I` | `--head` | `-n` | `--connections N` (download) |
| `-u` | `--user user:password` | `-j` | `--jobs N` |

Values have units: `30s`, `500ms`, `2M`, `1GiB`, `2M/s`. `K`, `M` and `G` are 1024-based, as in curl, wget and aria2; `kB` and `MB` are 1000-based. Every on/off option has a `--no-` form.

### Defaults

ghola follows redirects (up to 20), fails on status 400 and above (exit 22; stdout keeps the body, `-o` writes no file), retries a transient failure 3 times for GET, HEAD, PUT, DELETE and OPTIONS, and asks for gzip, br and zstd. `--no-follow`, `--no-fail`, `-r 0` and `--no-compressed` turn them off. Binary data to a terminal is refused, as in curl; `-o -` writes it anyway.

### Protocols

HTTP and HTTPS (with `--impersonate` on the tls-client stack), `file://`, `ftp://` and `ftps://`, `sftp://` and `scp://`, and `ws://` and `wss://`. Proxies: `http://`, `https://`, `socks4://`, `socks4a://`, `socks5://` and `socks5h://`, and the `http_proxy`, `https_proxy`, `ftp_proxy`, `ALL_PROXY` and `no_proxy` variables.

### Browser-like fetching

| Option | What it does |
|---|---|
| `--impersonate PROFILE` | Copy a browser's TLS and HTTP/2 fingerprint: `chrome`, `firefox`, `safari`, `edge`. |
| `--impersonate-headers` | Send the browser's default headers in its order (on with `--impersonate`). |
| `--impersonate-accept-language VALUE` | Override the profile's `Accept-Language`. |
| `--wait-jitter DURATION` | Wait a random time up to DURATION before each request. |
| `--referer none\|auto\|URL` | Set the Referer; `auto` sends the previous URL after a redirect. |
| `--chain eth\|base\|solana` | JSON-RPC shortcut: POST with the RPC headers of the chain. |

### Moving from v0

v1.0.0 removes the v0 flags. A v0 flag stops with an argument error that names its v1 form, for example `--serve is a ghola v0 flag. In v1 it is ghola serve.` The full table is in [docs/cli-v1.md](docs/cli-v1.md#9-migration-from-v0). Scripts in curl syntax can run as they are with `ghola curl`.

## Installation

### Homebrew (macOS / Linux)

```bash
brew install robot-accomplice/tap/ghola
```

To upgrade:

```bash
brew upgrade ghola
```

### Quick Install Script

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

### Linux Packages (rpm / deb / apk / Arch)

Native packages are attached to each [release](https://github.com/robot-accomplice/ghola/releases) and install `ghola` to `/usr/bin`:

```bash
# Fedora / RHEL / openSUSE
sudo dnf install ./ghola_<version>_linux_amd64.rpm

# Debian / Ubuntu
sudo apt install ./ghola_<version>_linux_amd64.deb

# Alpine
sudo apk add --allow-untrusted ghola_<version>_linux_amd64.apk

# Arch
sudo pacman -U ghola_<version>_linux_amd64.pkg.tar.zst
```

Replace `<version>` with the release version (e.g. `0.4.0`) and `amd64` with `arm64` on ARM hosts.

### Build from Source

```bash
git clone https://github.com/robot-accomplice/ghola.git
cd ghola
go build -o ghola ./cmd/ghola
```

Ghola now targets Go 1.27 for development and release builds.

## Usage

```bash
# Fetch a URL (redirects are followed, an HTTP error exits 22)
ghola https://httpbin.org/get

# Browser-like fetch with a pure-Go Chrome fingerprint
ghola --impersonate chrome https://example.com

# Keep cookies across runs, in the Netscape format of curl and wget
ghola --impersonate firefox --cookie-jar ~/.config/ghola/cookies.txt https://example.com

# Send through a SOCKS proxy that resolves the names
ghola --proxy socks5h://127.0.0.1:9050 https://example.com

# POST JSON-RPC to a chain node, with retries
ghola --chain base -r 3 -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' https://rpc.base.org

# Check the security posture of a site
ghola inspect https://example.com

# Download a file under its remote name, in 5 parallel ranges
ghola download -n 5 https://example.com/big.iso

# Resume a partial download; If-Range starts again if the file changed
ghola download -c https://example.com/big.iso

# Run a pasted curl command, or see its ghola form
ghola curl -sSL -H 'Accept: application/json' https://example.com/api
ghola curl --show-native -sSL -o out.zip https://example.com/out.zip
```

### Exit codes

ghola uses curl's exit codes for the same causes, so a script written for curl reads them the same way.

| Code | Cause |
|------|-------|
| 0 | Success |
| 1 | A redirect went to a protocol that ghola does not follow (for example `file://`) |
| 2 | Invalid or missing argument |
| 3 | A URL glob (`{a,b}` or `[1-9]`) is not valid (`--no-url-glob` turns globs off) |
| 6 | The host name did not resolve |
| 7 | The connection failed (for example, refused) |
| 22 | The server answered with status 400 or above (`--no-fail` turns it off) |
| 23 | ghola could not write the output (stdout or the `-o` file) |
| 26 | ghola could not read a local file (`-d @file`, `--upload`, `--form name=@file`) |
| 28 | A time limit ended the transfer (`--timeout`, `--timeout-connect`, `--stall-time`) |
| 32 | The body does not match `--checksum` or the server's `Content-Digest` (aria2's code; curl does not use 32) |
| 35 | The TLS handshake failed |
| 37 | ghola could not read the file of a `file://` URL |
| 67 | The FTP server refused the login |
| 78 | The FTP file does not exist |
| 47 | Redirects reached `--follow-max` |
| 55 | The request failed for another cause |
| 56 | The connection failed after it was ready (for example, a reset, or a TLS alert after the handshake) |
| 60 | The server certificate did not verify, or the SSH host key is not in `~/.ssh/known_hosts` |
| 63 | The body is larger than `--max-size` |
| 61 | The `Content-Encoding` of the answer cannot be decoded (`--no-compressed` gets it as it is) |
| 90 | The server public key does not match `--tls-pin` |
| 91 | The server sent no good OCSP staple (`--tls-ocsp`) |

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
