# ghola v1.0.0 release notes (draft)

**Status (2026-11-20): draft.** v1.0.0 is not released. The command line follows the v1 spec draft (`docs/cli-v1.md`, PR 65), which waits for Jon's review. Phases 2 to 4 are not complete; section 5 lists what is open.

## 1. Breaking changes

1. **The v0 flags are gone.** A v0 flag stops with an argument error (exit 2) that names its v1 form, for example `--serve is a ghola v0 flag. In v1 it is ghola serve.` The table of every v0 flag is in `docs/cli-v1.md`, section 9.
2. **Jobs are words.** `ghola download`, `ghola serve`, `ghola inspect`, `ghola profiles`, `ghola explain`, `ghola completion`. `ghola --serve` has no alias.
3. **New defaults** (decided by Jon, 2026-11-18, `docs/cli/v1-defaults.md`):
   - Redirects are followed, up to 20. `--no-follow` turns them off.
   - Status 400 and above fails with exit 22. stdout keeps the body; `-o` writes no file. `--no-fail` turns it off. (v0 `-f` refused every status that is not 2xx.)
   - A transient failure is retried 3 times for GET, HEAD, PUT, DELETE and OPTIONS (1, 2 and 4 seconds apart, or as `Retry-After` says). `-r 0` turns it off.
   - Responses are decompressed (gzip, br, zstd), except for a byte range.
   - Binary output to a terminal is refused, as in curl; `-o -` writes it.
   - The progress meter shows only when stderr is a terminal and the body does not go to it.
4. **Values have units.** `--timeout 30s` (a bare number is seconds; v0 `--timeout` was milliseconds). `K`, `M` and `G` are 1024-based (v0 `--limit-rate 1k` was 1000 bytes).
5. **`-q` keeps the body and the errors.** v0 `-s` wrote nothing at all.
6. **Exit codes are curl's** (6, 7, 22, 23, 26, 28, 35, 37, 47, 55, 56, 60, 61, 67, 78, and 1 for a refused redirect). `ghola explain CODE` describes each one.
7. **A new cookie jar is in the Netscape format**, as curl and wget write it. A v0 JSON jar is read and written back as JSON.
8. **`--ghost` is removed.** It sent an `X-Ghola-Identity` header that marked each request as coming from ghola. The bridge still accepts the `ghost` field and ignores it.
9. **scope-bca 0.8.11 and older** start the bridge with `ghola --serve` and lose ghola mode with v1.0.0. scope-bca 0.8.12 (released 2026-11-19) starts `ghola serve`.

## 2. New

1. **Compatibility commands:** `ghola curl`, `ghola wget` and `ghola aria2c` run the syntax of those tools with their defaults. `--show-native` prints the ghola command. The parity harness runs each of them on the arguments of the tool it copies; none fails a scenario that its tool passes.
2. **Protocols:** `file://`, `ftp://` and `ftps://`, `sftp://` and `scp://` (the host key must be in `~/.ssh/known_hosts`), `ws://` and `wss://`.
3. **Proxies:** SOCKS4, SOCKS4a, SOCKS5 and SOCKS5h on both stacks, and `ALL_PROXY`.
4. **Resume checks the version:** a partial download keeps `<file>.ghola` with the `ETag` or `Last-Modified`, and a resume sends `If-Range`. If the file changed, ghola starts again from byte 0. curl, Wget2 and aria2 join old and new bytes.
5. **Errors that teach:** a v0 flag, a curl, wget or aria2 flag, or a typo gets the native form.
6. **Shell completion** for bash, zsh, fish and PowerShell.

## 3. Fixed (defects of v0.8.0)

1. Credentials (`-u`, `Authorization`, `Cookie`) went to another host after a redirect (H1).
2. The cookie jar stored a cookie for any `Domain`, so one host could plant a cookie for another; it also stored cookies for public suffixes and sent host-only cookies to subdomains.
3. `--cookie` and `--cookie-jar` did nothing on a download to a file.
4. `--compressed -o file.tar.gz` unpacked a `.gz` file that the server sent as it is.
5. The stdout body was not byte-exact, `-n` repeated a POST, and `-r -1` crashed the bridge (H2, H4, H5).
6. The audit findings H1 to H8 and M1 to M9 are all fixed; `docs/audits/2026-11-12-v0.7.0-vs-curl-aria2-wget.md` has the list.

## 4. Release steps (not done)

1. Jon approves the v1 spec (PR 65) and the parser (PR 80).
2. Merge `develop` into `main` with a `--no-ff` commit.
3. Push the annotated tag `v1.0.0` with these notes; GitHub releases in this repository cannot be changed after that.
4. Dispatch the Homebrew workflow by hand: `gh workflow run homebrew.yml -f tag=v1.0.0`.

## 5. Open before v1.0.0

- Phase 2 options that the spec names and the parser refuses as "not built yet", for example `--json`, `--http`, `--resolve`, `--auth`, `--tls-min`, `--output-template`, `--cond-*`.
- Phase 3: the job engine (`--jobs`, `ghola jobs`, mirrors, checksums, Metalink, BitTorrent, RPC).
- Phase 4: `ghola mirror` (recursion, link conversion, robots.txt, WARC).
- Explicit FTPS (`AUTH TLS` on `ftp://`) and active FTP.
