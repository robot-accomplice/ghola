# v1 defaults: decisions for review

Phase 0B, step 3 (`docs/plans/2026-11-15-parity-and-ux-plan.md`, section 5A).

Decision D4 (A) applies: the native command uses ghola's defaults. `ghola curl`, `ghola wget` and `ghola aria2c` use the defaults of the tool they copy. So each item below changes the native command only.

Each item gives the curl default, the ghola v0 default, and lettered options. The recommendation is option A. Jon decides each item.

The curl facts come from the curl 8.18.0 manual (`curl --manual`), except where an item says otherwise.

## 1. Follow redirects

- curl: off. `-L` turns it on.
- ghola v0: off on stdout. On for a download to a file (`-o`), without `-L`. The two paths do not agree (M5 note).
- A. On for every path. `--no-follow` turns it off. The H1 rule stays: `-u`, `Authorization` and `Cookie` go to the first host only, unless `--location-trusted`.
- B. Off for every path, as in curl.

## 2. Redirect limit

- curl: 50.
- ghola v0: 10 (`config.DefaultMaxRedirs`).
- Chrome and Firefox: 20.
- A. 20, as the browsers that `--impersonate` copies.
- B. Keep 10.
- C. 50, as in curl.

## 3. Fail on HTTP errors

- curl: off. curl writes the error body and exits 0. `-f` exits 22 and writes no body. `--fail-with-body` exits 22 and writes the body.
- ghola v0: `-f` off. Without `-f`, `-o` on a non-2xx status writes the body and exits 22.
- A. On. A status at or above the item 4 limit exits 22. On stdout, ghola writes the body (curl's `--fail-with-body`). On `-o`, ghola writes no file, so an error page does not replace a download. `--no-fail` writes the body and exits 0.
- B. Off, as in curl: write the body and exit 0.
- C. On, and write no body on stdout either (curl's `-f`).

## 4. Which status is an HTTP error

- curl: 400 and above.
- ghola v0: any status that is not 2xx (`config.OutputOptions.FailsOn`).
- With item 1 B or `--no-follow`, the v0 rule fails on every 301, 302 and 304. A 304 answer to a conditional request is a success.
- A. 400 and above, as in curl.
- B. Keep "not 2xx".

## 5. Binary output to a terminal

- curl: refuses when stdout is a terminal and the body looks binary. `-o -` overrides this.
- ghola v0: writes the bytes to the terminal. No check.
- A. Refuse, as in curl. The message names the override: `-o -`, or `-o <file>`.
- B. Write the bytes, as v0 does.

## 6. Retry transient errors

- curl: off. `--retry <n>` retries a timeout and status 408, 429, 500, 502, 503, 504, 522 and 524. A refused connection needs `--retry-connrefused` too.
- ghola v0: off. `-r <n>` retries a request that failed before a response. `--retry-http` adds retryable status codes. ghola waits for `Retry-After`.
- A. On: 3 retries, for idempotent methods only (GET, HEAD, PUT, DELETE, OPTIONS). Same status codes as curl's `--retry`, plus a refused or reset connection. Exponential backoff from 1 s, or `Retry-After` when the server sends it. `--retry-max 0` turns it off. A POST retries only with an explicit `--retry-max`.
- B. Off, as in curl.

A retry of a POST can repeat a side effect, as H4 did. That is why option A excludes POST.

## 7. Decompress responses

- curl: off. `--compressed` sends `Accept-Encoding` and decodes the body.
- ghola v0: off. `--compressed` sends gzip only.
- A. On: send `Accept-Encoding: gzip, br, zstd` and decode. `--no-compressed` turns it off. A segmented download (`-n`) sends `identity`, because byte ranges of an encoded body cannot be joined. `--limit-rate` still counts the bytes on the wire (M3).
- B. Off, as in curl.

With `--impersonate`, the profile sets `Accept-Encoding`. Option A must decode what the profile asks for.

## 8. Progress meter

- curl: shows the meter when the body goes to a file or a pipe, also when stderr is not a terminal.
- ghola v0: shows the meter for `-o` only when stderr is a terminal (M4).
- A. Show the meter on stderr only when stderr is a terminal, and only when the body does not go to that terminal. `--progress` forces it. `--no-progress` turns it off.
- B. Keep v0: `-o` only.
- C. As in curl.

## 9. Timeouts

- curl: no total limit. No connect limit from the user (libcurl has an internal 300 s connect limit).
- ghola v0: no limit.
- A. `--connect-timeout 30s` by default. No total limit, so a large download is not stopped.
- B. No limit, as in curl.

## 10. Several URLs: what a failure does

- curl: goes on with the next URL, and exits with the last error. `--fail-early` stops at the first.
- ghola v0: stops at the first failure (M9).
- A. Keep v0: stop at the first failure. `--keep-going` goes on, and exits with the first error.
- B. As in curl.

## 11. `--serve`

- v1 makes the job a word: `ghola serve` (principle 1).
- scope-bca: its fallback is merged (scope-bca PR 235, `SERVE_ARGS_V1 = ["serve"]`).
- A. No alias. `--serve` stops with an argument error that names `ghola serve`, as plan step 8 says for every v0 flag.
- B. Keep `--serve` as a hidden alias in v1.

## 12. The v1.0.0 tag and scope-bca

- A. Tag v1.0.0 when ghola is ready. scope-bca already has its fallback.
- B. Wait for a scope-bca release that holds the fallback.

## Not in this list

- The size unit (`k` as 1000 or 1024). Principle 4 puts units in values, so the spec (step 4) decides it with all units.
- How `-o` pairs with several URLs. The spec decides it with the output options.
