# Plan: match curl, aria2 and wget, and give a better user experience than all three

Date: 2026-11-15. Base: ghola v0.8.0 (`main` at `c157e0f`).
Input: the audit `docs/audits/2026-11-12-v0.7.0-vs-curl-aria2-wget.md`.
Status: DRAFT. Decisions D1 to D5 are made (2026-11-15). Nothing in this plan is started.

## 1. Goal

ghola does each job that curl, aria2 and wget do, with a result that is the same or better. It is also easier to use than any of the three.

"The same or better" is a measured claim, not an opinion. A feature counts as done only when the parity harness (section 4) shows all of these:

1. **Result.** The bytes on stdout, the files written and the state of the files after a failure are the same as the best reference tool, or better.
2. **Exit.** The exit code tells the cause of the failure at least as precisely as curl.
3. **Cost.** Throughput, peak memory and request count are no worse than the best reference tool, within 10%.
4. **Hostile input.** The harness's hostile servers give a result that is no worse than the best reference tool.

"Better experience" is also measured. Section 6 lists each improvement and its check.

## 2. Where ghola is today

ghola has about 55 flags. curl has about 270. The table shows the state for each area. "Have" means the feature exists. It does not mean the feature has passed the parity harness.

| Area | ghola v0.8.0 | Best reference |
|---|---|---|
| HTTP/1.1 methods, data, forms, upload | Have (`-X -d -F --data-binary --data-urlencode -T`) | curl |
| Byte-exact stdout | **Broken** (audit H2) | curl |
| Redirects | Have, but credentials leak (H1) and the limit exits 0 (H6) | curl |
| HTTP/2 | Only through the impersonation transport (tls-client) | curl |
| HTTP/3 | `--http3`, only through the impersonation transport | curl |
| TLS verification, `--cacert`, `-k`, client certificates | Have | curl |
| TLS version, cipher, pinning, OCSP, ECH flags | Missing | curl |
| Browser impersonation (TLS and HTTP/2 fingerprint) | **Have, better than all three** | none |
| Cookies | JSON jar only. curl and wget use the Netscape format | curl |
| Proxies | HTTP proxy, proxy pools. No SOCKS flag, no `HTTP_PROXY` (M8), no `--noproxy` | curl |
| Name resolution control (`--resolve`, `--connect-to`, `-4/-6`, DoH) | Missing | curl |
| Auth | Basic only. No Digest, NTLM, Negotiate, AWS SigV4, `--netrc` | curl |
| Retries | `-r --backoff --retry-http` | curl |
| Timeouts | One timeout, and `-o` ignores it (H3) | curl |
| Resume | Have, checked since v0.8.0 | curl, aria2 |
| Segmented download from one URL | Have (`-n`), checked since v0.8.0 | aria2 |
| Several URLs, URL globbing, input file of URLs | Missing | curl, aria2, wget |
| Several mirrors for one file | Missing | aria2 |
| Checksum check | Missing | aria2 |
| Saved session and restart of a download queue | Missing | aria2 |
| Metalink | Missing | aria2, Wget2 |
| BitTorrent and magnet links | Missing | aria2 |
| RPC control of a running downloader | Bridge only (`--serve`), with no download control | aria2 |
| Recursive download, mirror, link conversion, robots.txt | Missing | wget |
| Time-stamping (`-N`, `-z`), ETag save and compare | Missing. `-R` sets the file time | wget, curl |
| WARC output | Missing | wget 1.x |
| `--write-out`, `--trace`, HAR | HAR only (`--har`). `--timing` | curl |
| Protocols other than HTTP(S) | None | curl (about 25 protocols) |
| Config file | Missing | curl, wget, aria2 |
| Rate limit | `-o` only. stdout ignores it (M3) | curl |
| Progress meter | Noisy and wrong (M4) | curl |

## 3. Decisions for Jon before work starts

These decisions change the size and order of the plan. Each has a recommendation.

**D1. Protocol scope.** curl supports about 25 protocols.
- A. All of them, including DICT, GOPHER, IMAP, LDAP, MQTT, POP3, RTMP, RTSP, SMB, SMTP, TELNET and TFTP.
- B. HTTP(S), WebSocket, FTP(S), SFTP, SCP and FILE. These are the protocols that wget and aria2 also use for downloads, plus SSH transfer.
- C. HTTP(S) and WebSocket only.
- **DECIDED by Jon, 2026-11-15: B.** It covers every download job of the three tools. The protocols that A adds are mail, directory and streaming protocols. They do not fit a fetcher, and each one adds its own security surface. Record A as "not planned" with this reason. Do not leave it as "later".

**D2. Flag structure. DECIDED by Jon, 2026-11-15:** the next release is a major version. It gets a new flag structure, designed for the most intuitive use. Section 5A describes the design work. The conflict table below shows one reason for the change: ghola's current letters conflict with curl's. The options below are kept as a record. They are no longer open.

8 short flags mean different things in ghola and curl:

| Flag | ghola | curl |
|---|---|---|
| `-r` | `--retry` | `--range` |
| `-b` | `--backoff` | `--cookie` |
| `-c` | `--chain` | `--cookie-jar` |
| `-D` | `--drift` | `--dump-header` |
| `-G` | `--ghost` | `--get` |
| `-S` | `--snoop` | `--show-error` |
| `-w` | `--wget` | `--write-out` |
| `-n` | `--connections` | `--netrc` |

A user who pastes a curl command (for example from the browser's "Copy as cURL") gets a different request with no warning.
- A. Give these letters curl's meaning in a major version (v1.0.0). Keep the ghola long names. Before that release, print a warning when one of the 8 letters is used.
- B. Keep the ghola meanings. Add a `--compat curl` mode, or a `curl` program name, that uses curl's meanings.
- C. Keep everything as it is.
- Superseded by the decision above. The new design keeps the main benefit of A: a pasted curl command still works, through the compatibility commands in section 5A. It does this without forcing curl's letters onto the native flags.

**D3. One HTTP engine or two.** ghola has two transports: fasthttp (HTTP/1.1 only) and tls-client (fhttp, with HTTP/2, HTTP/3 and impersonation). Every feature in this plan must work on each transport that stays.
- A. Move to one engine: the fhttp/tls-client stack for all requests. Use a plain Go TLS profile when impersonation is off.
- B. Keep both, and test each feature on both.
- **Benchmark result (2026-11-15, `test/bench/engines_test.go`, Windows, in-process server, median of 5):**
  - ghola has **three** stacks, not two. Without `--impersonate`, `Do` (body to stdout) uses fasthttp, and `Stream` (download to a file) uses net/http. With `--impersonate`, both use tls-client (fhttp). The split explains H8 (`-u` only in the `Do` path) and part of M8 (`HTTP_PROXY` only in the net/http path).
  - 256 MiB over HTTP: fasthttp 2.01 s (it buffers the whole body: the cause of M2), net/http 202 ms, tls-client 199 ms.
  - 256 MiB over HTTPS: fasthttp 393 ms, net/http 360 ms, tls-client 391 ms (+8.6% compared with net/http; the samples overlap).
  - 1 KiB requests: fasthttp 86 to 90 µs, net/http 114 µs, tls-client 128 to 136 µs. The difference is about 40 µs for each request, which is small compared with one network round trip.
  - tls-client has no plain Go TLS profile. With one engine, a request without `--impersonate` would need a custom profile (utls `HelloGolang`), or it would carry a browser fingerprint.
  - tls-client 1.15.1 supports a custom CA and client certificates (`TransportOptions.RootCAs`, `Certificates`). The comment in `internal/transport/transport.go` that says it does not is out of date.
- C. Remove fasthttp only. Use net/http for all requests without impersonation, and fhttp/tls-client for `--impersonate`. fhttp is a fork of net/http, so one adapter serves both. A request without impersonation keeps the plain Go TLS fingerprint.
- ~~DECIDED by Jon, 2026-11-15: A, after a benchmark.~~ Superseded below. The benchmark passed the 10% download test, but A needs a custom TLS profile to keep the plain Go fingerprint.
- **DECIDED by Jon, 2026-11-15 (after the benchmark): C.** fasthttp goes. It is the only stack that buffers the body (M2), and it cannot do HTTP/2 or HTTP/3. net/http and fhttp share one request path, so a flag cannot again reach only one stack (the cause of H8 and part of M8). The parity harness and the unit tests run each feature with and without `--impersonate`.

**D4. Default behaviour changes.** Some curl defaults are bad for users. For example, curl does not follow redirects. (Correction, 2026-11-18: an earlier text said curl writes binary data to a terminal. The curl 8.18.0 manual says curl refuses it unless `-o -` is given.) ghola can choose better defaults, but every changed default breaks a pasted curl command.
- A. The native command uses ghola's own defaults. The compatibility commands (`ghola curl`, `ghola wget`, `ghola aria2c`) use the defaults of the tool they copy.
- B. Use curl's defaults everywhere.
- **DECIDED by Jon, 2026-11-15: A.** Each individual default is still Jon's decision, in section 5A, step 3. The new flag structure (D2) means native commands do not need to copy curl. Pasted commands go through a compatibility command, so they keep the behaviour their authors expected. The defaults to choose are in section 5A, step 3.

**D5. BitTorrent.** BitTorrent needs a DHT node, peer connections and seeding. These make ghola a peer in a public network, not only a client.
- A. Full support: DHT, PEX, magnet links, seeding, web seeds.
- B. Download only: magnet links and `.torrent` files, with web seeds, and a fixed seeding ratio after the download.
- C. None.
- **DECIDED by Jon, 2026-11-15: B.** It matches the aria2 download job. Full seeding control is a separate product.

## 4. Phase 0: the parity harness

The audit's harness existed only in a session scratchpad. Port it into the repo as a Go test. Every later phase depends on it.

**Status (2026-11-15):** steps 1 to 3 are done in `test/parity/` (branch `test/parity-harness`). 54 scenarios run on Windows against curl 8.18.0, Wget2 2.2.1 and aria2 1.37.0. The known-failure list in `test/parity/known_test.go` holds 33 ghola failures. The harness found a new finding, H8, and corrected M4 in the audit report. Steps 4 and 5 are in CI since PR robot-accomplice/ghola#41: a `parity` job on `ubuntu-24.04`, `macos-15` and `windows-2025`. Pinned images fix the reference tool versions.

1. Add `test/parity/`. It holds a Go test server with the audit's hostile cases: ignored Range, wrong `Content-Range`, truncated body, hang, redirect chains, cross-host redirect, gzip, `Content-Disposition` traversal, flaky 503, self-signed TLS.
2. Add a runner. It runs one scenario on ghola and on each installed reference tool. It records exit code, stdout hash, stderr, the files written, the server's request log, the time and the peak memory.
3. Write the results as JSON, one record for each tool and scenario. A report command compares ghola with the best reference tool for each scenario.
4. Run the harness in CI on Linux, macOS and Windows. The audit tested Windows only.
5. Pin the reference tool versions in the harness. A new curl version changes the reference, so update the pin in its own change.

Exit criteria: the harness reproduces each audit finding from H1 to M9, and each reproduction fails on v0.8.0.

RCA data (Rule 13): each scenario record keeps the raw server log and the exact stdout bytes, so a failure can be checked after the run.

## 5A. Phase 0B: the new flag structure (major version)

The flag structure is designed before phases 2 to 4 add about 200 options. Each option added under the old structure would have to be renamed later.

### Design principles

Each principle comes from a known problem in curl, wget or aria2.

1. **The job is a word, not a flag.** `ghola <url>` fetches. Other jobs are subcommands: `ghola download`, `ghola mirror`, `ghola jobs` (queue, pause, resume), `ghola serve`, `ghola explain`. Today wget's `-r`, curl's `-Z` and aria2's `-i` each change the whole job with one letter.
2. **One concept, one prefix.** Related options share a prefix: `--tls-*`, `--proxy-*`, `--retry-*`, `--cookie-*`, `--auth-*`, `--output-*`, `--impersonate-*`. curl has `--cacert`, `--capath`, `--ciphers`, `--tlsv1.2` and `--ssl-reqd` for one concept.
3. **Values instead of flag families.** `--http 2` and not `--http2`, `--http2-prior-knowledge`, `--http3-only`. `--ip 6` and not `-6`. `--tls-min 1.2`, not `--tlsv1.2`.
4. **Units in values.** `--timeout 30s`, `--limit 2MB/s`, `--max-size 1GB`. Today ghola uses milliseconds, curl uses seconds, and wget uses both.
5. **Every boolean has a `--no-` form.** A config file default can then be turned off on the command line.
6. **Few short letters, all mnemonic.** Give letters only to the most common options. Choose the letters from measured use, not from habit (step 2). Each letter has one meaning in all subcommands.
7. **Same name in every mode.** `--retry-max` means the same thing for fetch, download and mirror.
8. **Errors teach.** An unknown flag shows the nearest match. A curl or wget flag in a native command shows the native name: "`-L` is a curl flag. In ghola, redirects are on by default. Use `--no-follow` to turn them off."

### Compatibility commands

`ghola curl <curl arguments>`, `ghola wget <wget arguments>` and `ghola aria2c <aria2 arguments>` accept the other tools' syntax unchanged. Each one also accepts `--show-native`. That flag prints the equivalent native command and does not run it, so users learn the new flags from commands they already know.

These commands replace the old D2 options A and B. A pasted "Copy as cURL" command works as `ghola curl …`. The native flags keep a clean design.

### Steps

1. **Inventory.** Generate a list of every option of curl, wget, Wget2, aria2 and ghola v0.8.0 from each tool's help output. Do this with a script, not by hand (Rule 6). Map each option to one concept, for example "TLS minimum version" or "retry count".
   **Status (2026-11-15):** the list is done, without the concept map. `scripts/flag_inventory.py` reads each tool's help output and writes `docs/cli/flag-inventory.csv`. It found 716 options: ghola 57, curl 8.22.0 274, Wget2 2.2.1 188, aria2 1.37.0 197. Together they have 636 different long names. Only 5 long names occur in all four tools (`--header`, `--help`, `--referer`, `--user-agent`, `--version`). 35 short letters mean different things in curl and wget, for example `-c` (`--cookie-jar` and `--continue`) and `-H` (`--header` and `--span-hosts`). One letter set cannot copy both tools, which supports the compatibility commands.
2. **Measure use.** Count how often each option occurs in real commands: the "Copy as cURL" output of the browsers, the READMEs and scripts of public repos, and this repo's docs. The most frequent options get short letters (principle 6).
3. **Decide defaults.** List each default where ghola can differ from curl: follow redirects, fail on HTTP errors, refuse binary output to a terminal, retry transient errors, decompress responses, and show progress only on a terminal. Jon decides each one.
   **Status (2026-11-18):** the list for review is in `docs/cli/v1-defaults.md`, 12 items with lettered options. No item is decided yet.
4. **Draft the spec.** Write `docs/cli-v1.md`: each subcommand, each option, its type, its unit, its default, and the curl, wget and aria2 equivalents.
5. **Task test.** Write 40 common tasks, for example "resume a download", "post JSON with a bearer token" and "mirror a site to depth 2". For each task, compare the native command with curl, wget and aria2: number of options, characters, and how many tasks need the documentation. The native command must be the shortest or tie for each task. Where it is not, change the spec.
6. **Review.** Jon reviews the spec and the task results before any code changes.
7. **Build.** Replace `config.ParseFlags` with the new parser. This also clears the SonarCloud S3776 complexity finding. Build the compatibility commands on the same option table, so they cannot drift from the native flags.
8. **Migration.** There is no interim release (section 9), so no release prints warnings for v0 flags before they go. v1.0.0 removes the v0 flags. When v1.0.0 sees a v0 flag, it stops with an argument error that gives the new name. The release notes have a v0-to-v1 table. `ghola curl` is the stable path for scripts that use curl syntax.

Exit criteria: the spec is approved, the task test passes, and every v0.8.0 flag has a mapping, or a recorded reason to remove it.

## 5. Phases 1 to 5: features

Phases 2 to 5 use the new flag structure. The curl flag names in those phases name the feature that must exist. They are not the flag that ghola will use.

Each phase starts with harness scenarios that fail. The phase ends when they pass. A phase can start before the previous phase ends, but it cannot claim parity before the harness passes.

### Phase 1: correct HTTP (the audit's open findings)

Parity claims are not possible while the basic fetch is wrong. Fix these first, in this order:
0. H8: send `-u` credentials on the download path too. It breaks a normal use, so it goes first.
   - Status (2026-11-16, branch `fix/h8-download-auth`): done. Both paths set their headers in one function, `setHeaders` (`internal/client/headers.go`). `-u` goes only to the scheme, host and port of the URL the user gave, as in curl. This also fixes the `-u` part of H1 (`xhost_basic` passes).
1. H2 and M2: stream the body to stdout byte-exact. Send `-v` and all other metadata to stderr. `-i` stays on stdout, as in curl (decided by Jon, 2026-11-17; the earlier text put `-i` on stderr).
   - Status (2026-11-17, branch `fix/h2-stdout-stream`): done. `Do` returns at the headers and leaves the body in the `fasthttp.Response` as a body stream (`SetBodyStream`). `ProcessResponse` copies it to stdout unchanged, and `-i` writes the exact header block. `-v`, the ghost signature, the profile lines and `Total:` go to stderr. `--jq`, `--har` and the bridge read the whole body through `transport.ReadBody`, which reports a broken body. `rsp.Body()` does not: it puts the error text in place of the body.
   - `--timeout` moved out of `FetchURL` into `client.WithTimeout`, which the caller holds until the body is read. The deadline now covers the body, as curl's `--max-time` does, and the `-o` path too. This fixes the core of H3. The separate curl timeout flags (step 3) are still open.
2. H1: drop `Authorization`, `Cookie` and `-u` credentials on a cross-host redirect. Add `--location-trusted`. The `-u` part is done (step 0). A custom `Authorization` or `Cookie` header (`xhost_bearer`) still follows the redirect.
   - Status (2026-11-17, branch `fix/h1-cross-host-headers`): done. `setHeaders` drops the `Authorization` and `Cookie` headers from `-H` on a redirect to another scheme, host or port, as it does for `-u`. `--location-trusted` sends all three to every host and turns on `-L`, as in curl. `xhost_bearer` passes. The new scenario `xhost_trusted` matches curl.
3. H3: apply `--timeout` to every path. Add curl's separate `--connect-timeout`, `--max-time`, `--speed-limit` and `--speed-time`.
   - Status (2026-11-18, branch `fix/h3-timeout-flags`): done. The four flags use curl's names and units. `--max-time` sets the same limit as `--timeout`, so the two together are an error. `--connect-timeout` ends at the `GotConn` trace event, after DNS, TCP, the proxy CONNECT and TLS, on net/http and on tls-client. A server that sends its headers late is not stopped. `--speed-limit` and `--speed-time` use curl's defaults (30 s, 1 byte/s). The speed check starts with the request, so the wait for the headers counts: curl 8.18.0 does the same (checked against a server that holds its headers for 3 s). One file, `internal/transport/limits.go`, holds the limits for both transports. The short letters (`-m` and the others) wait for the phase 0B flag design. Exit code 28 for these errors is M6.
4. H4: make `-n` apply only to segmented downloads. Report every failed request and set the exit code.
   - Status (2026-11-18, branch `fix/h4-concurrency`): done. `RunConcurrent` (race n copies of the request, keep the first answer) is removed. Without `-o`, `-n` sends one request and prints a note on stderr, so a failure gives the normal error exit. A GET with a body (`-X GET -d`) is not segmented. `n_post` and `n_refused` pass.
5. H5: reject a negative `-r`.
   - Status (2026-11-18, branch `fix/h5-negative-retry`): done. `-r -1` is an argument error. `FetchURL` also refuses a negative count, because the bridge passes its JSON `retries` field there without the flag check. Before the fix, `"retries": -1` crashed the bridge process with a nil-pointer panic.
6. H6 and H7: the redirect-limit exit code, and `-f -o` writes no file.
   - Status (2026-11-18, branch `fix/h6-h7-redirect-limit-fail-file`): done. `-L` past `--max-redirs` returns `ErrTooManyRedirects` on stdout and on `-o` (curl's exit 47 comes with M6). On `-o`, the file opens only when the response is accepted: `-f` on an HTTP error writes no file, and a refused response no longer truncates an existing file. `-f` gives the same exit code with `-o` as on stdout.
   - Open, for the v1 flag design (D4 defaults): ghola's `-f` refuses any non-2xx status, and curl's `-f` refuses only 400 and above. `config.OutputOptions.FailsOn` holds ghola's rule in one place. Also, `-o` without `-f` writes an error body and exits non-zero, and curl writes it and exits 0.
7. M1: send no default `Content-Type` on GET. Send `application/x-www-form-urlencoded` for `-d`, as curl does.
   - Status (2026-11-18, branch `fix/m1-content-type`): done. The `-H` flag has no default. `-d`, `--data-binary` and `--data-urlencode` add `application/x-www-form-urlencoded` when `-H` gives no `Content-Type`. `--chain` adds `application/json`, because it is a JSON-RPC shortcut. The bridge never used the CLI default (`Headers: nil`), so bridge requests do not change. Breaking: a script that posts JSON with `-d` must add `-H 'Content-Type: application/json'` (or `--json` in phase 2).
8. M3 to M9: rate limit on stdout, progress meter, the extra HEAD request, exit codes, input checks, proxy variables, URL handling.
   - Status (2026-11-18, branch `fix/m3-rate-stdout`): M3 done. `output.bodyReader` passes the body through `client.LimitReader`, so `--limit-rate` applies to stdout and to the buffered `-o` path (POST, `--jq`, `--har`). The limit counts the bytes on the wire, before the `--compressed` decoder, as in curl. `rate_stdout` passes. Not changed: ghola reads `k` as 1000 and curl reads it as 1024; the v1 flag design owns the unit.
   - Status (2026-11-18, branch `fix/m4-progress-meter`): M4 done. A download to a file prints the meter only when stderr is a terminal (`golang.org/x/term`), as Wget2 and aria2 do. curl prints it to a file too, so here ghola is quieter than curl. The total comes from the response that is written, not from the probe. With `--compressed` the total is not known, so the meter shows only the byte count. The meter prints a final line at the end. `progress_not_tty` and `progress_total` pass.
   - Status (2026-11-18, branch `fix/m5-no-head`): M5 done. A download to a file sends no HEAD. Without `-n`, it sends one GET. The GET follows redirects itself: `StreamMeta` now carries `Location`, so `resolveTarget`, `resolveTargetViaGet` and `streamLocation` are removed. With `-n`, a 1-byte ranged GET (`bytes=0-0`) finds the size and the Range support. A 206 answer proves Range support, and its `Content-Range` gives the size. The probe stops after the headers, so a server that ignores Range does not send the whole file to it. The download path did not retry: `retry_503` passed only because the two HEAD requests used up the server's 503 answers. Now the single GET retries as `FetchURL` does: `-r` retries a request that failed before it had a response, and `--retry-http` retries a retryable status without writing its body. It does not retry after the file opened. It waits for `Retry-After` when the server sends it, as `FetchURL` does. `no_head` passes. Not changed, for the v1 flag design: `-o` follows redirects without `-L`, and curl does not.
   - Status (2026-11-18, branch `fix/m7-input-checks`): M7 done. `ParseFlags` refuses a negative `--timeout`, `-n` below 1, and a `-H` value without a colon or a name, with an argument error (exit 2) before any request. `-H "Name:"` removes the header, as in curl: `setHeaders` deletes it, and the profile and the defaults do not set it. Both engines added their own User-Agent (`Go-http-client`) when the request had none; the transport now stops that. One gap: tls-client (fhttp) over HTTP/1 sends an empty `User-Agent:` line, because the value that removes it there makes fhttp's HTTP/2 encoder panic. `config.ParseHeader` is the one `-H` parser. Not done, for phase 2 (curl HTTP parity): curl's `-H "Name;"` form, which sends an empty header. `neg_timeout`, `zero_connections`, `header_no_colon` and `header_remove` pass.
   - Status (2026-11-18, branch `fix/m8-proxy-env`): M8 done. `transport.proxyFromEnv` reads `http_proxy`/`HTTP_PROXY` for http, `https_proxy`/`HTTPS_PROXY` for https, and skips the hosts in `no_proxy`/`NO_PROXY` (`*`, host, domain suffix, IP, CIDR). Unlike Go's `http.ProxyFromEnvironment`, it proxies loopback hosts, as curl and wget do, and reads the environment on each request. `HTTP_PROXY` is ignored when `REQUEST_METHOD` is set (httpoxy), as in Go. net/http calls it per request; tls-client (`--impersonate`) read no proxy variables, and now sets its proxy per request with `SetProxy`. Also fixed: `--proxy` and `--proxy-file` did not apply to a download to a file (`DefaultStreamer` built its transport without a proxy), so `-o` went direct. `selectProxy` now serves both paths. Not done, for phase 2 (curl HTTP parity): `ALL_PROXY` and `--noproxy`. Unused: `client.DefaultDoer` still builds a transport without a proxy; no production code calls it. `proxy_env` passes.
   - Status (2026-11-18, branch `fix/m9-urls`): M9 done. A URL without a scheme gets `http://`, as in curl. `url.Parse` cannot decide this, because it reads `localhost:8080` as the scheme `localhost`, so the rule is "no `://`". Every URL on the command line is kept (`Options.URLs`). ghola fetches them one after the other and writes each body to stdout, as curl does. `--max-time` covers each URL on its own. The first failure stops the run and gives the exit code; curl goes on and returns the last error, and `--fail-early` is curl's way to stop. A second URL with `-o`, `-O`, `-w`, `-J` or `--har` is an argument error: curl takes one `-o` for each URL, and the v1 flag design decides how ghola pairs them. `url_no_scheme` and `two_urls` pass.
9. Exit codes: adopt curl's exit code numbers for the same causes (6, 7, 22, 23, 28, 35, 47, 60, and the others). This is a breaking change, so it goes into the same major version as D2.
   - Status (2026-11-18, branch `fix/m6-exit-codes`): done, with M6. `config.ExitCode` holds curl's numbers: 2 (argument), 6, 7, 22, 23, 26 (local file read), 28, 35, 47, 55 (other request failure) and 60. Follow-up (branch `fix/m7-input-checks`): 56 for a failure after the connection was ready (the `GotConn` trace event, after TLS). An end-to-end case failed 1 run in 40 on CI. The captured errors were a reset or an abort on read (net/http and tls-client), or a TLS alert that fhttp turns into text. curl 8.18.0 exits 56 for this server in 400 of 400 runs, so the expected 35 was wrong. The transport now marks such errors `ErrRecv`. One function, `exitFor` (`cmd/ghola/exit.go`), maps an error to its code on stdout and on `-o`. `config.ErrWrite` marks a failed write, so a write error (23) is not mixed up with a body that broke off. `-o` without `-f` on a non-2xx status now exits 22, not 4 (the D4 question, exit 0 as in curl, is still open). Both stacks give the same codes. One case stays at 55: a server that closes the connection during the TLS handshake gives a plain read error on both stacks, and curl gives 35. `exit_refused`, `exit_dns`, `exit_tls` and `exit_timeout` pass. The README has the table.

### Phase 2: curl HTTP parity

1. HTTP versions: `--http1.0 --http1.1 --http2 --http2-prior-knowledge --http3 --http3-only` on every transport that stays (D3).
2. Name resolution and connections: `--resolve`, `--connect-to`, `-4`, `-6`, `--interface`, `--local-port`, `--unix-socket`, `--doh-url`, Happy Eyeballs.
3. TLS: `--tlsv1.2 --tlsv1.3 --tls-max`, `--ciphers`, `--pinnedpubkey`, `--cert-status`, `--crlfile`, ECH, client certificate types. Keep impersonation as a ghola-only feature on top.
4. Auth: Digest, NTLM, Negotiate, `--oauth2-bearer`, `--aws-sigv4`, `--netrc` and `--netrc-file`.
5. Proxies: SOCKS4, SOCKS4a, SOCKS5, SOCKS5h, HTTPS proxy, `--noproxy`, `--proxy-*` TLS flags, and the proxy environment variables.
6. Cookies: read and write the Netscape format, so jars work with curl and wget. Keep the JSON jar as a ghola format.
7. Requests: `-G`, `--url-query`, `--json`, `--form-string`, `--variable`, `--path-as-is`, `--request-target`, `-I`, `--head`.
8. Output: `-w/--write-out` with curl's variables and `%{json}`, `-D/--dump-header`, `--trace`, `--trace-ascii`, `--trace-time`, `--create-dirs`, `--output-dir`, `--max-filesize`, `--no-clobber`, `--remove-on-error`.
9. Conditional requests: `-z/--time-cond`, `--etag-save`, `--etag-compare`, and `If-Range` on every resume. The audit found that no tested tool checks `If-Range`, so this is a point where ghola can do better than all three.
10. Several URLs: more than one URL, `-:/--next`, URL globbing (`{a,b}` and `[1-9]`), `-Z/--parallel`, `--parallel-max`.
11. HSTS and Alt-Svc caches: `--hsts`, `--alt-svc`.
12. Config file: read `-K/--config` in curl's syntax, and `~/.config/ghola/config`.

### Phase 3: aria2 download-manager parity

1. A job engine: a queue of downloads with a maximum number of active jobs (`-j`), and a global rate limit.
2. Input file: `-i` with one URL on each line and options for each job, in aria2's format.
3. Several mirrors for one file: split the segments across the mirrors, and choose the faster mirror based on measured speed.
4. File allocation: `none`, `prealloc`, `falloc`, `trunc`.
5. Checksums: `--checksum=sha-256=<hex>` for the whole file, and piece hashes from Metalink. Check before the download is reported as done.
6. A saved session: write a state file next to each partial download. Restart all unfinished jobs with one command. aria2 has this, and curl and wget do not.
7. Metalink v3 and v4 (RFC 5854), including mirrors, piece hashes and signatures.
8. BitTorrent, as decided in D5.
9. RPC: JSON-RPC over HTTP and WebSocket to add, pause, stop and list jobs. Put it behind the v0.8.0 bridge checks (Host, token, Content-Type). Do not add a second listener with a weaker design.
10. Hooks: run a command when a job finishes or fails (`--on-download-complete`).
11. FTP(S), SFTP and SCP sources, if D1 is B.

### Phase 4: wget parity

1. Recursive download: `-r`, `-l`, `-np`, `-H`, `-D`, `-A/-R`, `--accept-regex/--reject-regex`, `-I/-X` directory lists.
2. Mirror: `-m`, `-N` time-stamping, `-nc`, `-x/-nd/-nH`, `--cut-dirs`.
3. Page requisites and link conversion: `-p`, `-k`, `-K`, `-E`.
4. robots.txt and `nofollow`, on by default as in wget. `-e robots=off` turns them off.
5. Politeness: `--wait`, `--random-wait`, `--quota`.
6. Input from HTML: `-F -i page.html`, and sitemap parsing as in Wget2.
7. `--spider` mode to check links without saving them.
8. File names: `--restrict-file-names`, `--content-disposition`, `--adjust-extension`.
9. WARC output (`--warc-file`). This matters for forensic users, because WARC is the standard archive format for web captures.
10. Background mode (`-b`) with a log file.
11. wget's `-e` and `.wgetrc` options, mapped to the ghola config file.

wget's short flags conflict with curl's in many places (for example `-O`, `-c`, `-N`). ghola cannot follow both. Use curl's letters (D2). Keep wget's long names, and add a `ghola mirror` mode that uses wget's letters if user demand shows a need. This keeps one flag language for the main command.

### Phase 5: other protocols (D1)

For each protocol in the chosen scope: upload, download, resume, list, and the same exit codes as curl. Each protocol gets harness scenarios first, as in the other phases.

## 6. Better experience than all three

Each item has a check. None of these changes a curl default (D4).

| Improvement | Why it is better | Check |
|---|---|---|
| Pasted curl, wget and aria2 commands work | `ghola curl …` runs the browser's "Copy as cURL" output unchanged (section 5A) | The harness replays a set of "Copy as cURL" commands from Chrome, Firefox and Safari |
| `--show-native` teaches the new flags | No other tool translates commands | A test for each compatibility command: the native command it prints gives the same harness result |
| One tool for fetch, download manager and mirror | Users learn one flag language, not three | Every phase 3 and 4 feature uses the same flag names as phase 2 |
| Error messages say what failed, why, and what to try | curl prints a code and a short line. wget and aria2 print log lines | Each exit code has one message test |
| "Did you mean" for unknown flags | curl says only "option unknown" | A test for each long flag with one typo |
| `ghola explain <exit code>` | Users do not need the curl man page to read an exit code | A test for each code |
| Progress only on a terminal, with correct totals | Fixes M4. aria2's summary lines are hard to read | Harness checks: no progress output when stderr is a file. Correct total with `--compressed` and retries |
| A multi-job display that shows each job on one line | aria2 shows one summary line. wget shows one job at a time | Manual check on Linux, macOS and Windows terminals |
| Machine output: `--json-events` writes one JSON line for each transfer event | curl's `--write-out` is a template language. aria2 needs RPC | A JSON schema in the repo. A test validates the output against it |
| Every interrupted download can restart | curl and wget lose a queue. aria2 needs `--save-session` | Harness: kill the process during a job, run it again, compare the file |
| Resume checks `If-Range` | All 4 tested tools mixed old and new content | Harness scenario: the file changes between the first request and the resume |
| Integrity by default where the server gives a digest (`Digest`, `Content-Digest`, Metalink) | No reference tool checks `Content-Digest` | Harness scenario with a wrong digest |
| `--help <topic>` and shell completion for bash, zsh, fish and PowerShell | curl has `--help category`, but no completion in the binary | A test for each shell's completion script |
| Browser impersonation in every mode (fetch, download, mirror) | None of the three can do it | Harness check against a local JA3/JA4 fingerprint server |

## 7. Risks

1. **Size.** curl took more than 25 years to reach its feature set. This plan is very large. The phases let each part ship on its own. Phase 1 is useful even if later phases never start.
2. **Compatibility commands drift from the native flags.** Build both on one option table (section 5A, step 7). The harness runs each compatibility scenario, and also the native command that `--show-native` prints.
2a. **Users of v0 flags break.** With one release there are no early warnings. The argument errors that name the new flag, the v0-to-v1 table and the `ghola curl` path reduce this. The bridge clients (scope, scope-bca) use JSON fields, not flags. Check that the new structure does not rename those fields, or tell the scope-bca session first.
2b. **Audit fixes wait for v1.0.0.** H1 (auth headers sent to another host after a redirect) is a credential leak in v0.8.0, and it stays in the released binary until v1.0.0. **DECIDED by Jon, 2026-11-15: no v0.8.x patch.** H1 waits for v1.0.0 with the other fixes.
3. **Two transports.** D3 is C: net/http and fhttp stay. Every feature needs a test with and without `--impersonate`. One shared request path keeps the two in step.
4. **Binary size.** BitTorrent, SSH and WARC add dependencies. Measure the binary size in CI. Set a limit with Jon before phase 3.
5. **Security surface.** Each new protocol, the RPC interface and BitTorrent add attack paths. Each one needs its own adversarial review before release, as C1 showed.
6. **Reference drift.** curl changes often. The pinned reference versions (phase 0, step 5) keep the parity claims honest.
7. **Impersonation conflicts.** Some curl flags (for example `--ciphers`, `--http1.1`) contradict a browser profile. The rule: an explicit flag wins, and ghola prints one warning that the fingerprint no longer matches the profile.

## 8. What is not planned, and why

- **curl's mail, directory and streaming protocols** (if D1 is B): they do not fit a fetcher, and each one adds a security surface with no download use.
- **`--libcurl` code output:** ghola is not a library that other programs link to, so there is no code to generate.
- **BitTorrent seeding control beyond a fixed ratio** (if D5 is B): it is a separate product, a torrent client, not a downloader.

## 9. Order of work

1. D1 to D5 are decided. The D3 benchmark is done (D3 is C). Remove fasthttp first: the phase 1 fixes go into the shared net/http and fhttp request path.
   - Status (2026-11-16, branch `refactor/net-http-engine`): the fasthttp client is gone. Without `--impersonate`, `Do` and `Stream` use one net/http client and one request builder (`newHTTPRequest`). `--buffer-size` now only raises net/http's 1 MiB response header limit. The v1 spec decides if it stays.
   - Still on fasthttp: the `fasthttp.Request` and `fasthttp.Response` types (about 70 call sites) and the `--serve` bridge server. Moving the bridge server is not required for D3: it is a server, not a client engine.
   - RETRACTED (2026-11-17): this item said the M2 fix must replace the types, "because a streamed body cannot live in a `fasthttp.Response`". That is wrong: fasthttp v1.73.0 has `Response.SetBodyStream`, and `ReleaseResponse` closes the stream. Jon chose to keep the types for M2 (2026-11-17). Replacing them is no longer a dependency of any phase 1 step.
2. Phase 0: the harness. Phase 0B (the flag design) runs at the same time, because the design work does not change code.
**Release rule. DECIDED by Jon, 2026-11-15: one release.** All parity and UX work ships together as v1.0.0: phases 1 to 5, the new flag structure, the compatibility commands and the exit-code change. There are no interim releases. The steps below merge into `develop` one at a time, but none of them is tagged until all are done.

3. Phase 1: the audit fixes, in the current flags. They merge into `develop` and are not released alone.
4. Build the new flag structure and the compatibility commands, with the exit-code change.
5. Phase 2, in the new flag structure.
6. Phases 3 and 4. They can run in parallel, because they touch different packages, but both use the phase 3 job engine. Build the job engine first.
7. Phase 5.
8. Section 6 items go with the phase that adds the code they depend on, not at the end.
9. Release v1.0.0 when the parity harness shows no known ghola failure for a planned feature (`known_test.go` is empty for planned items) and Jon approves.
