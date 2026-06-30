# Streaming downloads + curl-compatible flags — design

**Date:** 2026-06-30
**Status:** Approved (brainstorm), pending implementation plan
**Scope:** ghola (`github.com/robot-accomplice/ghola`)

## Problem

ghola buffers entire HTTP response bodies in memory and cannot stream large
downloads to disk. The whole pipeline is built around `fasthttp.Response`, which
holds the full body in RAM (`rsp.Body()`):

- `simpleTransport.Do` → `fasthttp.Client.Do` reads the whole body into `rsp`.
- `tlsClientTransport.Do` → `io.ReadAll(httpResp.Body)` then `rsp.SetBody(body)`
  (`internal/transport/transport.go:137`).
- `output.ProcessResponse` then does `os.WriteFile(file, body)` — a second full
  copy (`internal/output/output.go:43`).

Consequences (observed 2026-06-30 pulling 20–28 GB GGUFs on strixfeind, v0.4.0):

- A single 28 GB download needs ≥28 GB RAM even at `-n 1`.
- `-n N` (concurrent connections, "first success wins" in
  `client.RunConcurrent`, `internal/client/client.go:442`) spawns N goroutines
  that each fully buffer the body before one wins — `ghola -n 8 -L -o file
  <28GB>` consumed **49.4 GB RAM + 4.5 GB swap** then was **OOM-killed**.

Secondary gap: ghola is missing many common curl flags, most importantly resume
(`-C`/`--continue-at`) for interrupted large downloads.

## Goals

1. Stream large downloads to disk with bounded memory (the headline OOM fix).
2. Make `-n N` actually accelerate large downloads via HTTP Range segmentation.
3. Add resume and the download-ergonomics curl flags.
4. Add the request/TLS curl-compatibility flags.

## Non-goals / decisions

- **Flag-letter divergences left as-is.** ghola keeps `-c`=`--chain`,
  `-G`=`--ghost`, `-b`=`--backoff` (which collide with curl's
  cookie-jar/get/cookie). Realigning would break existing scripts and the
  roboticus.ai install docs. Every *new* flag below uses a letter currently free
  in ghola, so no conflict is forced. The divergences get a `--help` note.
- **Bridge contract untouched.** `bridge.Request`/`bridge.Response` (consumed by
  scope, unversioned) is CLI-orthogonal; the streaming path is CLI-only and does
  not change bridge structs.

## Architecture: streaming coexists with buffering

The buffered pipeline stays for requests that need the whole body in memory:
`--jq`, `--snoop`, `--har`, and stdout output. A request routes to the **new
streaming download path** only when **all** hold:

- output is a file (`-o`/`--output` set, or `-w`/`--wget` inferred a filename),
- method is GET,
- none of `--jq`, `--snoop`, `--har` are set.

Everything else is unchanged, so existing tests and behavior are preserved.

### Transport streaming capability

Extend the `Transport` interface (`internal/transport/transport.go`) with a
streaming method alongside `Do`:

```go
// StreamMeta carries the response line + headers needed by the download path.
type StreamMeta struct {
    StatusCode    int
    ContentLength int64   // -1 if unknown
    AcceptRanges  bool
    LastModified  string
    Disposition   string  // Content-Disposition
    FinalURL      string  // after redirects, if the transport resolved them
}

Stream(ctx context.Context, req *fasthttp.Request, sink io.Writer) (StreamMeta, error)
```

- `simpleTransport`: `fasthttp.Client{StreamResponseBody: true}`, then
  `resp.BodyWriteTo(sink)` — body never materializes.
- `tlsClientTransport`: `io.Copy(sink, httpResp.Body)` instead of `io.ReadAll`.
- Redirects are handled by the download orchestrator (below), reusing the
  existing `handleRedirect` / `isRedirectStatus` helpers — `Stream` itself does
  not follow redirects.

## Download orchestrator — `internal/client/download.go`

1. **Probe.** HEAD the URL (following redirects via the existing helpers) to learn
   final URL, `Content-Length`, `Accept-Ranges`, `Content-Disposition`,
   `Last-Modified`. If HEAD is unsupported, fall back to a `Range: bytes=0-0`
   GET probe.
2. **Mode select.**
   - **Segmented** when `-n>1` AND `-o` AND `Accept-Ranges: bytes` AND a known
     `Content-Length` AND not `--compressed`: `file.Truncate(size)` to
     preallocate, split into N contiguous byte ranges (remainder on the last
     segment), spawn N goroutines, each issuing `Range: bytes=s-e` and streaming
     through `io.NewOffsetWriter(file, s)`. Memory ≈ N × copy-buffer.
   - **Single-stream** otherwise: one connection, `io.Copy` to the file.
3. **Error handling.** Any segment error cancels the errgroup and returns the
   first error; the partial file is left on disk (resumable in the single-stream
   case).

## Resume, rate limiting, ranges, naming

- **`-C` / `--continue-at`** (value `-` = auto from current file size):
  single-stream only. Stat the file, send `Range: bytes=off-`, require a `206`
  (error if the server returns `200`, matching curl), append from the offset.
  **Segmented + `-C` falls back to single-stream resume** with a logged notice.
- **`--limit-rate`** (`2M`, `500k`, `1G`): parse to bytes/sec; aggregate
  `golang.org/x/time/rate` limiter on the write path (curl's aggregate
  semantics, shared across segments).
- **`--range`**: explicit `Range` header passthrough; single-stream; partial
  write. Mutually exclusive with `-C`.
- **`-O` / `--remote-name`**: filename = URL path basename (generalize the
  existing `inferWgetFilename`).
- **`-J` / `--remote-header-name`**: filename from `Content-Disposition`
  (requires probe/response headers; sanitize to basename, reject path traversal).
- **`-R` / `--remote-time`**: `os.Chtimes` from `Last-Modified` after download.
- **Progress.** Minimal throttled progress line to stderr (bytes / total / rate);
  suppressed under `-s`/`--silent`.

## Request / TLS curl-compatibility flags

- **`-k` / `--insecure`**: `tls.Config{InsecureSkipVerify: true}` wired into both
  transports (fasthttp `TLSConfig`; tls-client `WithInsecureSkipVerify`).
- **`--cacert` / `--cert` / `--key`**: custom CA pool + client certificate built
  into the `tls.Config` for both transports.
- **`--compressed`**: send `Accept-Encoding: gzip`, decode (`gzip.Reader` on the
  stream / `BodyGunzip` on the buffered path). Incompatible with segmentation
  (compressed length ≠ stored length, ranges meaningless). **gzip only.**
- **`-F` / `--form`**: `multipart/form-data` body (`name=value`, `name=@file`);
  sets `Content-Type` with boundary.
- **`--data-binary`**: like `-d` but no newline stripping; `@file` read verbatim.
- **`--data-urlencode`**: URL-encode the supplied data.

## Component placement

- `internal/transport/transport.go`: `Stream` method on the interface + both
  impls; `tls.Config` construction for `-k`/`--cacert`/`--cert`/`--key`.
- `internal/client/download.go`: orchestrator (probe, segmented, single-stream,
  resume); reuses redirect/retry helpers from `client.go`.
- `internal/client/ratelimit.go` (or under `download.go`): rate-limited /
  progress-reporting `io.Writer` wrappers.
- `internal/config/config.go`: new flags, `Options` fields, and validation
  (`--range` ⊻ `-C`; `--compressed` disables segmentation; `-F` ⊻ `-d`).
- `cmd/ghola/main.go`: route to `client.Download` when the streaming-path
  predicate holds; otherwise the existing buffered path.

## Options additions (sketch)

```go
type OutputOptions struct {
    // ...existing...
    RemoteName   bool    // -O
    RemoteHeader bool    // -J
    RemoteTime   bool    // -R
    ContinueAt   string  // -C ("-", or a byte offset)
    Range        string  // --range
    LimitRate    string  // --limit-rate (raw; parsed to bytes/sec)
}

type StealthOptions struct {
    // ...existing...
    Insecure   bool   // -k
    CACert     string // --cacert
    ClientCert string // --cert
    ClientKey  string // --key
    Compressed bool   // --compressed
}

// request body
Form        []string // -F (repeatable)
DataBinary  string   // --data-binary
DataURLEnc  []string // --data-urlencode (repeatable)
```

## Testing & gates

Local test server serving a known blob **with Range support**:

- Single-stream and segmented downloads both reconstruct the source checksum.
- Resume from a truncated partial → full checksum; offset math verified.
- Range-ignoring server (returns `200` to a `Range` request) → clean error
  (resume) or single-stream fallback (segmentation).
- `--limit-rate` → measured elapsed ≥ expected lower bound.
- `--compressed` → gzip server body decoded correctly; segmentation refused.
- `-O`/`-J` filename derivation; `-R` mtime applied; `--range` partial bytes.
- Unit tests: range-splitting (incl. remainder + tiny files), rate parsing,
  filename/`Content-Disposition` sanitization, resume-offset calc.

New code meets the existing **≥80%-per-package** CI coverage gate and the
no-regression-below-baseline rule.

## Justified deferrals (Rule 16)

- **Segmented resume** (per-segment sidecar progress file): deferred —
  single-stream resume already covers the "my 28 GB download dropped" case; the
  sidecar is meaningful added surface for a rarer need. Risk/scope tradeoff.
- **Brotli `--compressed`**: deferred to avoid pulling a new dependency; gzip
  covers the common case.

## Implementation phasing (within this single spec)

Ordered so the headline bug is fixed first and each step is independently
verifiable:

1. Streaming transport `Stream` + single-stream `Download` (fixes OOM at `-n 1`).
2. Segmented parallel download for `-n>1`.
3. Resume (`-C`) + `--range`.
4. Download ergonomics: `-O`, `-J`, `-R`, `--limit-rate`, progress.
5. Request/TLS flags: `-k`, `--cacert`/`--cert`/`--key`, `--compressed`, `-F`,
   `--data-binary`, `--data-urlencode`.
