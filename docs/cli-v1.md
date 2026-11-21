# ghola v1 command line

**Status (2026-11-20): draft for Jon's review** (phase 0B, step 6 of `docs/plans/2026-11-15-parity-and-ux-plan.md`). No code uses it yet. Section 11 lists the questions that need a decision.

The data is in three files in `docs/cli/`: `v1-options.csv`, `v0-to-v1.csv` and `v1-tasks.csv`. `python scripts/cli_spec.py` checks them, runs the task test and writes the tables in this file between the BEGIN and END markers. Do not edit those tables by hand. The parser (step 7) is built from `v1-options.csv`, so the spec and the code cannot drift.

Inputs:
- The design principles: plan section 5A.
- The defaults: `docs/cli/v1-defaults.md` (decided by Jon, 2026-11-18).
- The measured use of each option: `docs/cli/flag-usage.md`.
- Every option of curl, wget and aria2: `docs/cli/flag-inventory.csv`.

## 1. Jobs

The first word picks the job. Without a job word, ghola fetches.

| Command | Job | v0 form | Phase |
|---|---|---|---|
| `ghola URL...` | Fetch each URL. The body goes to stdout, or to `-o`. | `ghola URL` | 0B |
| `ghola download URL...` | Save each URL as a file under its remote name. Adds segments (`-n`), several jobs at once (`-j`), mirrors, checksums and a saved state for each job. | `-O`, `-w`, `-n` | 0B, 3 |
| `ghola mirror URL` | Follow links and copy a site, as `wget -m`. | none | 4 |
| `ghola jobs [list\|pause\|resume\|cancel\|clean]` | Manage the saved download jobs. `resume` restarts every unfinished job. | none | 3 |
| `ghola serve` | Run the local bridge on 127.0.0.1:18789. | `--serve` | 0B |
| `ghola inspect URL` | Check the headers and the security posture of a URL. | `-S`, `--snoop` | 0B |
| `ghola explain CODE` | Explain an exit code. | none | 0B |
| `ghola profiles` | List the impersonation profiles. | `--profile-list` | 0B |
| `ghola completion SHELL` | Write the completion script for bash, zsh, fish or PowerShell. | none | 0B |
| `ghola curl ARGS` | Run curl syntax with curl's defaults (section 6). | none | 0B, 2 |
| `ghola wget ARGS` | Run wget syntax with wget's defaults. | none | 0B, 4 |
| `ghola aria2c ARGS` | Run aria2 syntax with aria2's defaults. | none | 0B, 3 |

Rules:
1. The job word must be the first argument.
2. Options can come before or after the URLs, as in curl. `--` ends the options.
3. Each option has a scope: `all` (fetch, download, mirror and inspect), or one job. `ghola inspect` also takes the fetch options, because it makes a request as a fetch does (for example with `--impersonate` or `--proxy`). An option outside its scope is an argument error that names the job where it works.

## 2. Short letters

Principle 6 gives letters only to options with a measured use. 18 options have a letter. Each letter has one meaning in every job.

| Letter | Option | Why it has a letter |
|---|---|---|
| `-o` | `--output` | curl 49.7%, wget `-O` 70.5%, aria2c 57.9% of files |
| `-q` | `--quiet` | curl `-s` 64.4%, wget 54.2% |
| `-v` | `--verbose` | the same letter in all three tools |
| `-H` | `--header` | curl 11.2%, browser-header files 81.2% |
| `-d` | `--data` | curl 7.7% |
| `-m` | `--method` | curl `-X` 10.3%. `m` for method |
| `-I` | `--head` | curl 9.9% |
| `-u` | `--user` | the same letter in curl |
| `-c` | `--continue` | aria2c 34.6%, wget 5.7% |
| `-k` | `--no-tls-verify` | curl 2.4%, wget 9.7% |
| `-r` | `--retry-max` | curl 3.9%, wget 7.7%, aria2c 5.9%. v0 `-r` had the same meaning |
| `-t` | `--timeout` | curl 2.6% |
| `-w` | `--output-template` | curl 3.3% |
| `-i` | `--input` | wget 1.2%, aria2c 1.5%. Each queue task needs it |
| `-n` | `--connections` | aria2c `-x` 64.6%, `-s` 49.2% |
| `-j` | `--jobs` | aria2c 2.6% |
| `-h` | `--help` | convention |
| `-V` | `--version` | convention |

Letters can be grouped: `-qo out.zip` is `-q -o out.zip`. A letter that takes a value can hold it: `-n8`.

curl's `-L` and `-f` have no native letter, because redirects and failing on HTTP errors are on by default. curl's most common cluster, `-fsSL`, becomes `-q`.

## 3. Values

1. **Duration:** a number with `ms`, `s`, `m` or `h`, for example `500ms` or `30s`. A bare number is seconds, as in curl.
2. **Size:** a number with `B`, `K`, `M` or `G`. Section 11, question C, decides whether `K` is 1000 or 1024.
3. **Rate:** a size per second. `/s` is optional: `2M` and `2M/s` are the same.
4. **Boolean:** `--name` turns it on and `--no-name` turns it off. Each boolean has both forms (principle 5).
5. **List:** values separated by commas, for example `--retry-status 429,503`.
6. **Repeat:** an option marked "repeat" can be given more than once.
7. **Body source:** `@file` reads a file and `@-` reads stdin, for `--data` and `--json`.

## 4. Defaults

The native jobs use the defaults that Jon decided in `docs/cli/v1-defaults.md`. These are the defaults that differ from curl:

1. Redirects are followed, up to 20.
2. A status of 400 or above exits 22. stdout keeps the body, and `-o` writes no file.
3. Binary output to a terminal is refused, as in curl.
4. Transient errors are retried 3 times, for idempotent methods only.
5. Responses are decompressed.
6. The progress meter shows only when stderr is a terminal and the body does not go to it.
7. There are no default timeouts.
8. Several URLs go on after a failure, and the exit code is the last error, as in curl.

## 5. Options

The phase column says where each option is built. 0B is the parser build (step 7). For an option of phase 2 to 5, the name is fixed now, and the feature comes with its phase.

<!-- BEGIN options -->
### Output

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--output`, `-o` | path | stdout (fetch); remote name (download and mirror) | -o / -O / --output-dir | -O / -P | -o / -d | 0B | Write the body to PATH. A PATH that ends in / or names a folder gets the remote file name inside it. With several URLs: one -o DIR/ serves all, or one -o for each URL in order. |
| `--output-name` | url\|header | url | -J | --content-disposition | (default header) | 0B | Take the remote file name from the URL or from Content-Disposition. A name with a folder part is refused. |
| `--output-overwrite` | bool | true (fetch); false (download and mirror) | --no-clobber (inverse) | -nc (inverse) | --allow-overwrite | 2 | Replace an existing file. Without it an existing file stops the job with exit 23. |
| `--output-create-dirs` | bool | false | --create-dirs | -x | (default) | 2 | Create the missing folders of PATH. |
| `--output-remote-time` | bool | false | -R | (default with -N) | --remote-time | 0B | Set the file time to the Last-Modified time. |
| `--output-select` (fetch) | gjson path |  |  |  |  | 0B | Write only the value at the path of a JSON body (gjson syntax). |
| `--output-template`, `-w` | template |  | -w |  |  | 2 | After each transfer write the template. It uses curl's %{name} variables and %{json}. |
| `--output-events` | none\|json | none |  |  | (RPC) | 2 | Write one JSON line to stderr for each transfer event. A JSON schema in the repo defines the lines. |
| `--show-headers` (fetch) | bool | false | -i / --show-headers | -S (stderr) |  | 0B | Write the response header block to stdout before the body. |
| `--save-headers` | path |  | -D | --save-headers |  | 2 | Write the response header block to PATH. |
| `--quiet`, `-q` | bool | false | -sS | -q | -q | 0B | Write no progress meter and no notes. Errors still go to stderr. |
| `--verbose`, `-v` | bool | false | -v | -v | --console-log-level=debug | 0B | Write the request and response headers and the connection steps to stderr. |
| `--progress` | bool | auto | -# / --no-progress-meter | --progress / -nv | --summary-interval | 0B | Show the progress meter. Default: only when stderr is a terminal and the body does not go to it. |
| `--max-size` | size |  | --max-filesize | --quota (total) |  | 2 | Stop with exit 63 when the body is larger than SIZE. |
| `--har` (fetch) | path |  |  |  |  | 0B | Write the request and the response to PATH as HAR 1.2. |
| `--trace` | path |  | --trace / --trace-ascii |  |  | 2 | Write all bytes sent and received to PATH (- for stderr). |
| `--trace-format` | hex\|ascii | hex | --trace-ascii |  |  | 2 | The format of --trace. |
| `--trace-time` | bool | false | --trace-time |  |  | 2 | Add a time to each --trace and --verbose line. |

### Request

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--method`, `-m` (fetch) | string | GET; POST with a body | -X | --method |  | 0B | Send the request with this method. |
| `--head`, `-I` (fetch) | bool | false | -I | --spider -S |  | 0B | Send HEAD and write the response headers to stdout. |
| `--header`, `-H` | Name: value (repeat) |  | -H | --header | --header | 0B | Add a request header. "Name:" removes a header that ghola or the profile sets. |
| `--user-agent` | string | ghola/VERSION | -A | -U | -U | 0B | Set the User-Agent header. |
| `--referer` | none\|auto\|URL | none | -e | --referer | --referer | 0B | Set the Referer header. auto sends the previous URL after a redirect. |
| `--data`, `-d` (fetch) | text \| @file \| @- |  | --data-binary | --post-data / --post-file |  | 0B | Send the body without changes. @file reads a file and @- reads stdin. The Content-Type is application/x-www-form-urlencoded unless -H sets one. |
| `--data-literal` (fetch) | text |  | --data-raw | --post-data |  | 0B | Send the text as the body, also when it starts with @. |
| `--field` (fetch) | name=value (repeat) |  | --data-urlencode |  |  | 0B | Add a URL-encoded form field to the body. |
| `--form` (fetch) | name=value \| name=@file (repeat) |  | -F |  |  | 0B | Send a multipart/form-data body. |
| `--json` (fetch) | text \| @file \| @- |  | --json |  |  | 2 | Send a JSON body. Sets Content-Type and Accept to application/json. |
| `--query` | name=value (repeat) |  | --url-query / -G |  |  | 2 | Add a URL-encoded query parameter to each URL. |
| `--upload` (fetch) | path |  | -T | --body-file --method=PUT |  | 0B | Send the file as the body of a PUT. |
| `--range` | byte range |  | -r |  |  | 0B | Request only the byte range, for example 0-1023. |
| `--continue`, `-c` | bool | false | -C - | -c | -c | 0B | Resume a partial file. The request sends If-Range, so a file that changed on the server starts again from byte 0. |
| `--continue-at` (fetch) | bytes |  | -C N | --start-pos |  | 0B | Resume at this byte offset. |
| `--chain` (fetch) | eth\|base\|solana |  |  |  |  | 0B | JSON-RPC shortcut: POST with the RPC headers of the chain. |
| `--url-glob` | bool | true | -g (inverse) |  | -Z | 2 | Expand {a,b} and [1-9] in URLs. |
| `--url-path-as-is` | bool | false | --path-as-is |  |  | 2 | Send the path without removing ./ and ../. |
| `--request-target` (fetch) | string |  | --request-target |  |  | 2 | Send this request target in place of the URL path. |
| `--variable` (fetch) | name=value (repeat) |  | --variable |  |  | 2 | Set a variable for {{name}} expansion in other option values. |
| `--next` (fetch) | (separator) |  | -: / --next |  |  | 2 | Start the options for the next URL. |
| `--input`, `-i` | path |  | -K (partly) | -i | -i | 3 | Read URLs from PATH (- for stdin): one for each line, with options for each URL in aria2's format. |
| `--input-format` | lines\|html\|sitemap\|metalink | lines |  | -F / --force-html | (detected) | 3 | The format of --input. |

### HTTP

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--http` | auto\|1.0\|1.1\|2\|2-prior\|3\|3-only | auto | --http1.0 / --http1.1 / --http2 / --http2-prior-knowledge / --http3 / --http3-only | --no-http2 (Wget2) |  | 2 | The HTTP version. |
| `--compressed` | bool | true | --compressed | --compression (Wget2) | --http-accept-gzip | 0B | Ask for gzip, br and zstd and decode the body. A segment of a download asks for identity. |
| `--follow` | bool | true | -L | (default) | (default) | 0B | Follow redirects. |
| `--follow-max` | int | 20 | --max-redirs | --max-redirect |  | 0B | Stop with exit 47 after this many redirects. |
| `--follow-trusted` | bool | false | --location-trusted |  |  | 0B | Send -u and the Authorization and Cookie headers to other hosts after a redirect too. |
| `--fail` | bool | true | --fail-with-body | (default) | (default) | 0B | Exit 22 on status 400 and above. stdout keeps the body. -o writes no file. |
| `--hsts` | path |  | --hsts | --hsts-file |  | 2 | Read and write the HSTS cache at PATH. |
| `--alt-svc` | path |  | --alt-svc |  |  | 2 | Read and write the Alt-Svc cache at PATH. |
| `--cond-time` | [-]date \| file |  | -z | -N | --conditional-get | 2 | Send If-Modified-Since (or If-Unmodified-Since with -) for the date or the file time. |
| `--cond-etag-save` | path |  | --etag-save |  |  | 2 | Write the ETag of the response to PATH. |
| `--cond-etag-compare` | path |  | --etag-compare |  |  | 2 | Send If-None-Match with the ETag in PATH. |
| `--max-header-size` | size | 1MiB |  |  |  | 0B | The largest response header block that ghola accepts. |

### Time and speed limits

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--timeout`, `-t` | duration | none | -m / --max-time | -T | --timeout | 0B | Stop with exit 28 when one transfer takes longer. |
| `--timeout-connect` | duration | none | --connect-timeout | --connect-timeout | --connect-timeout | 0B | Stop with exit 28 when DNS + TCP + proxy + TLS take longer. |
| `--stall-time` | duration | 30s (with --stall-rate) | -y / --speed-time | --read-timeout (close) | --lowest-speed-limit (with a fixed time) | 0B | Stop with exit 28 when the speed is below --stall-rate for this long. |
| `--stall-rate` | rate | 1B/s (with --stall-time) | -Y / --speed-limit |  | --lowest-speed-limit | 0B | The speed limit for --stall-time. |
| `--rate-max` | rate |  | --limit-rate | --limit-rate | --max-download-limit | 0B | Limit the speed of each transfer. |
| `--rate-max-total` | rate |  |  |  | --max-overall-download-limit | 3 | Limit the speed of all transfers together. |
| `--wait` | duration | 0 |  | -w |  | 4 | Wait between requests. |
| `--wait-jitter` | duration | 0 |  | --random-wait |  | 0B | Add a random wait from 0 to this duration before each request. |

### Retries

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--retry-max`, `-r` | int | 3 (idempotent methods); 0 (others) | --retry | -t | -m | 0B | Retries after a transient failure. A POST retries only when this is set. |
| `--retry-delay` | duration | 1s (doubles each time) | --retry-delay | --waitretry | --retry-wait | 0B | The first wait before a retry. Retry-After from the server wins. |
| `--retry-max-time` | duration |  | --retry-max-time |  |  | 2 | Stop retries after this total time. |
| `--retry-status` | list | 408,429,500,502,503,504,522,524 | (fixed list) | --retry-on-http-error |  | 0B | The status codes that are retried. A refused or reset connection and a timeout are always retried. |

### Names and connections

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--ip` | any\|4\|6 | any | -4 / -6 | -4 / -6 | --disable-ipv6 | 2 | The IP version. |
| `--resolve` | host:port:addr (repeat) |  | --resolve |  |  | 2 | Use this address for host:port. |
| `--connect-to` | host:port:host2:port2 (repeat) |  | --connect-to |  |  | 2 | Connect to host2:port2 for requests to host:port. |
| `--interface` | name \| address |  | --interface | --bind-address | --interface | 2 | Send from this interface or address. |
| `--local-port` | port range |  | --local-port |  |  | 2 | Send from a local port in this range. |
| `--unix-socket` | path |  | --unix-socket |  |  | 2 | Connect through this Unix socket. |
| `--doh` | URL |  | --doh-url | --dns-over-https (Wget2) |  | 2 | Resolve names with DNS over HTTPS at this URL. |

### TLS

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--tls-verify`, `-k` | bool | true | -k (inverse) | --no-check-certificate (inverse) | --check-certificate | 0B | Check the server certificate. -k is --no-tls-verify. |
| `--tls-ca` | path | system store | --cacert | --ca-certificate | --ca-certificate | 0B | Trust the CA certificates in PATH. |
| `--tls-ca-dir` | path |  | --capath | --ca-directory |  | 2 | Trust the CA certificates in this folder. |
| `--tls-cert` | path |  | -E / --cert | --certificate | --certificate | 0B | Send this client certificate. |
| `--tls-key` | path |  | --key | --private-key | --private-key | 0B | The private key of --tls-cert. |
| `--tls-cert-type` | PEM\|DER\|P12 | PEM | --cert-type | --certificate-type |  | 2 | The format of --tls-cert. |
| `--tls-min` | 1.2\|1.3 | 1.2 | --tlsv1.2 / --tlsv1.3 | --secure-protocol | --min-tls-version | 2 | The lowest TLS version. |
| `--tls-max` | 1.2\|1.3 | 1.3 | --tls-max |  |  | 2 | The highest TLS version. |
| `--tls-ciphers` | list |  | --ciphers / --tls13-ciphers | --ciphers |  | 2 | The cipher suites. With --impersonate this changes the fingerprint and ghola writes a warning. |
| `--tls-pin` | sha256//hash (list) |  | --pinnedpubkey | --pinnedpubkey (Wget2) |  | 2 | Refuse a server key that does not match. |
| `--tls-crl` | path |  | --crlfile | --crl-file |  | 2 | Refuse certificates in this CRL. |
| `--tls-ocsp` | bool | false | --cert-status | --ocsp (Wget2) |  | 2 | Require a good OCSP staple. |
| `--tls-ech` | bool | false | --ech |  |  | 2 | Use Encrypted Client Hello when the server offers it. |

### Authentication

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--user`, `-u` | user:password |  | -u | --user / --password | --http-user / --http-passwd | 0B | Send these credentials. They go only to the host of the first URL unless --follow-trusted. |
| `--auth` | basic\|digest\|ntlm\|negotiate\|any | basic | --basic / --digest / --ntlm / --negotiate / --anyauth |  |  | 2 | The authentication method for --user. |
| `--auth-bearer` | token |  | --oauth2-bearer |  |  | 2 | Send Authorization: Bearer with the token. |
| `--auth-aws-sigv4` | provider:region:service |  | --aws-sigv4 |  |  | 2 | Sign the request with AWS Signature Version 4. |
| `--auth-netrc` | bool | false | -n / --netrc | (default on) | (default on) | 2 | Read credentials from ~/.netrc. |
| `--auth-netrc-file` | path |  | --netrc-file |  | --netrc-path | 2 | Read credentials from this netrc file. |

### Proxies

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--proxy` | URL | from the environment | -x | -e use_proxy=on | --all-proxy | 0B | Send through this proxy: http https socks4 socks4a socks5 socks5h (SOCKS in phase 2). |
| `--proxy-user` | user:password |  | -U | --proxy-user / --proxy-password | --all-proxy-user | 0B | The proxy credentials. |
| `--proxy-file` | path |  |  |  |  | 0B | Read proxy URLs from PATH (one for each line). |
| `--proxy-strategy` | sticky\|random\|round-robin | sticky |  |  |  | 0B | How --proxy-file picks a proxy for each request. |
| `--proxy-check` | bool | false |  |  |  | 0B | Check each proxy before use and drop dead ones. |
| `--proxy-bypass` | list | from no_proxy | --noproxy | (no_proxy) | --no-proxy | 2 | Hosts that go direct. |
| `--proxy-tls-*` | (as --tls-*) |  | --proxy-cacert / --proxy-insecure / ... |  |  | 2 | Each --tls-* option has a --proxy-tls-* form for an HTTPS proxy. |

### Cookies

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--cookie` | name=value \| file (repeat) |  | -b | --load-cookies | --load-cookies | 0B | Send this cookie, or the cookies in the file. |
| `--cookie-jar` | path |  | -c (write) with -b (read) | --load-cookies / --save-cookies | --load-cookies / --save-cookies | 0B | Read cookies from PATH and write them back. ghola reads the Netscape format and the v0 JSON jar. A new jar is Netscape. |

### Impersonation

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--impersonate` | profile |  |  |  |  | 0B | Copy a browser's TLS and HTTP/2 fingerprint: chrome firefox safari edge. ghola profiles lists them. |
| `--impersonate-headers` | bool | false |  |  |  | 0B | Send the browser's default headers in its order. |
| `--impersonate-accept-language` | string | profile default |  |  |  | 0B | Override the profile's Accept-Language. |

### Download manager

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--connections`, `-n` (download) | int | 1 |  |  | -x / -s | 3 | Split one file into N ranges on N connections. Needs Range support. |
| `--jobs`, `-j` | int | 1 | -Z / --parallel-max |  | -j | 3 | Run up to N transfers at the same time. |
| `--source` (download) | URL (repeat) |  |  |  | (several URIs on one line) | 3 | Another mirror of the same file. Segments go to the faster mirrors. |
| `--file-allocation` (download) | none\|prealloc\|falloc\|trunc | none |  |  | --file-allocation | 3 | How the file space is reserved before the download. |
| `--checksum` | algo=hex |  |  |  | --checksum | 3 | Check the file against this digest before the job is done. Content-Digest and Metalink digests are checked by default. |
| `--session` (download) | bool | true |  |  | --save-session | 3 | Keep a state file next to each partial download, so ghola jobs resume can restart it. |
| `--on-complete` (download) | command |  |  |  | --on-download-complete | 3 | Run this command when a job finishes. |
| `--on-error` (download) | command |  |  |  | --on-download-error | 3 | Run this command when a job fails. |
| `--seed-ratio` (download) | float | 1.0 |  |  | --seed-ratio | 3 | BitTorrent: stop seeding at this upload ratio (D5). |
| `--background` | bool | false |  | -b | (daemon) | 4 | Run in the background and write messages to --log. |
| `--log` | path | ghola.log with --background | --stderr | -o / -a | -l | 4 | Write messages to PATH. |

### Mirror

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--depth` (mirror) | int | 5 |  | -l |  | 4 | Follow links this many levels deep (0 = no limit). |
| `--parent` (mirror) | bool | false |  | -np (inverse) |  | 4 | Follow links above the start folder. |
| `--hosts` (mirror) | list | start host |  | -H / -D |  | 4 | The hosts that links may go to. |
| `--accept` (mirror) | glob list |  |  | -A |  | 4 | Keep only file names that match. |
| `--reject` (mirror) | glob list |  |  | -R |  | 4 | Skip file names that match. |
| `--accept-regex` (mirror) | regex |  |  | --accept-regex |  | 4 | Keep only URLs that match. |
| `--reject-regex` (mirror) | regex |  |  | --reject-regex |  | 4 | Skip URLs that match. |
| `--include-dirs` (mirror) | list |  |  | -I |  | 4 | Follow only these folders. |
| `--exclude-dirs` (mirror) | list |  |  | -X |  | 4 | Skip these folders. |
| `--layout` (mirror) | full\|no-host\|flat | full |  | -x / -nH / -nd |  | 4 | How the local folders copy the URL. |
| `--cut-dirs` (mirror) | int | 0 |  | --cut-dirs |  | 4 | Drop this many leading folders from the local path. |
| `--timestamps` (mirror) | bool | true |  | -N | --conditional-get | 4 | Get a file again only when the server copy is newer. |
| `--requisites` (mirror) | bool | false |  | -p |  | 4 | Also get the images and styles that a page needs. |
| `--convert-links` (mirror) | bool | false |  | -k |  | 4 | Change links to work offline. |
| `--backup-converted` (mirror) | bool | false |  | -K |  | 4 | Keep the original of each converted file. |
| `--adjust-extension` (mirror) | bool | false |  | -E |  | 4 | Add .html or .css when the type needs it. |
| `--offline` (mirror) | bool | false |  | -p -k -E |  | 4 | Make the copy work offline: --requisites, --convert-links and --adjust-extension together. |
| `--robots` (mirror) | bool | true |  | -e robots=off (inverse) |  | 4 | Obey robots.txt and nofollow. |
| `--quota` (mirror) | size |  |  | -Q |  | 4 | Stop after this many bytes in total. |
| `--spider` | bool | false |  | --spider | --dry-run | 4 | Check the links and save nothing. |
| `--file-names` | unix\|windows\|ascii | system |  | --restrict-file-names |  | 4 | Which characters a local file name may hold. |
| `--warc` | path |  |  | --warc-file |  | 4 | Also write the exchange as a WARC file. |

### General

| Option | Type | Default | curl | wget | aria2 | Phase | Description |
|---|---|---|---|---|---|---|---|
| `--config` | path | ~/.config/ghola/config | -K | --config / -e | --conf-path | 2 | Read options from PATH. |
| `--no-config` | bool | false | -q (first) | --no-config | --no-conf | 2 | Read no config file. |
| `--help`, `-h` | topic |  | -h / --help category | -h | -h | 0B | Show help, or the help for a topic. |
| `--version`, `-V` | bool | false | -V | -V | -v | 0B | Show the version. |
<!-- END options -->

## 6. Compatibility commands

`ghola curl`, `ghola wget` and `ghola aria2c` accept the syntax of the tool they copy, with that tool's defaults (D4). For example, `ghola curl URL` does not follow redirects.

1. **One table.** Each compatibility option maps to a native option through the curl, wget and aria2 columns of `v1-options.csv`. The parser for each compatibility command is generated from those columns, so a new native option is in the compatibility commands when its column is filled.
2. **`--show-native`** prints the native command and does not run it. The harness runs that native command too, and compares the results (plan risk 2).
3. **An option with no native form** is an argument error (exit 2) that names it. For example `--libcurl` and the mail protocols are not planned (plan section 8).
4. **No rc files.** The compatibility commands do not read `.curlrc`, `.wgetrc` or `aria2.conf`, so a pasted command runs the same on every computer. `ghola wget -e` and `ghola curl -K` read their arguments.

## 7. Errors that teach

Each case is an argument error (exit 2). The message names the fix.

1. **An unknown option** shows the nearest native name (edit distance 2 or less): "`--folow` is not a ghola option. Did you mean `--follow`?"
2. **A v0 flag** shows its v1 form from `v0-to-v1.csv`: "`--serve` is a v0 flag. Use `ghola serve`."
3. **A flag of curl, wget or aria2** shows the native form from the equivalents columns: "`-L` is a curl flag. In ghola, redirects are on by default. Use `--no-follow` to turn them off."
4. **A letter that changed its meaning.** `-c`, `-i` and `-w` had other meanings in v0 (`--chain`, `--include`, `--wget`). A v0 command with these letters does not get the v0 message, because the letters are valid. Section 11, question E, decides this.

## 8. Exit codes, config and environment

1. **Exit codes** are curl's numbers, as in phase 1 (README table). 2 is an argument error. `ghola explain CODE` writes the cause and what to try.
2. **Config file:** `~/.config/ghola/config` (on Windows `%APPDATA%\ghola\config`). It holds one option on each line, in long form without the dashes, for example `follow-max = 10`. An option on the command line wins. `--config PATH` reads another file, and `--no-config` reads none.
3. **Environment:**
   - `http_proxy`, `https_proxy` and `no_proxy`, as in phase 1, M8.
   - `NO_COLOR` and `TERM=dumb` turn the colour off, as in v0.
4. **The bridge** (`ghola serve`) keeps its JSON fields. scope and scope-bca send JSON, not flags (plan risk 2a). Section 11, question A, covers the `ghost` field.

## 9. Migration from v0

v1.0.0 removes the v0 flags (plan step 8). A v0 flag stops with an argument error that names the v1 form. The release notes have this table.

<!-- BEGIN v0-to-v1 -->
| v0 | v1 | Note |
|---|---|---|
| `--accept-language` | --impersonate-accept-language |  |
| `-b / --backoff MS` | --retry-delay DURATION | The value has a unit: 1s and not 1000. |
| `--buffer-size N` | --max-header-size SIZE | It only raised the header limit after fasthttp went (plan section 9). |
| `-c / --chain NAME` | --chain NAME | No short letter: -c is --continue. |
| `--cacert` | --tls-ca |  |
| `--cert` | --tls-cert |  |
| `--compressed` | (default) | Use --no-compressed to turn it off. |
| `--connect-timeout SECONDS` | --timeout-connect DURATION | A bare number is still seconds. |
| `-n / --connections N` | -n / --connections N | Only for ghola download. |
| `-C / --continue-at OFFSET` | -c / --continue or --continue-at OFFSET | -C - becomes -c. |
| `--cookie` | --cookie |  |
| `--cookie-jar FILE` | --cookie-jar FILE | ghola reads the v0 JSON jar and writes it back as JSON. A new jar is Netscape. |
| `-d / --data` | -d / --data | v1 sends a file from @file without changes. v0 did the same. |
| `--data-binary` | -d / --data |  |
| `--data-urlencode` | --field |  |
| `-D / --drift MS` | --wait-jitter DURATION |  |
| `-f / --fail` | (default) | Use --no-fail to turn it off. The rule is now status 400 and above. |
| `-F / --form` | --form | No short letter (1.4% of curl files). |
| `-G / --ghost` | (removed) | It sent an X-Ghola-Identity header that marked each request as coming from ghola. |
| `--har` | --har |  |
| `-H / --header` | -H / --header |  |
| `-h / --help` | -h / --help |  |
| `--http3` | --http 3 |  |
| `--impersonate` | --impersonate |  |
| `-i / --include` | --show-headers | curl 8.10 uses the same name. |
| `-k / --insecure` | --no-tls-verify |  |
| `--jq PATH` | --output-select PATH | The syntax is gjson and not jq. See open question 2. |
| `--key` | --tls-key |  |
| `--limit-rate` | --rate-max |  |
| `-L / --location` | (default) | Use --no-follow to turn it off. |
| `--location-trusted` | --follow-trusted |  |
| `--max-redirs` | --follow-max | The default is now 20. |
| `--max-time SECONDS` | --timeout DURATION |  |
| `-o / --output` | -o / --output |  |
| `--profile-list` | ghola profiles | The job is a word. |
| `--proxy` | --proxy |  |
| `--proxy-auth` | --proxy-user |  |
| `--proxy-check` | --proxy-check |  |
| `--proxy-file` | --proxy-file |  |
| `--proxy-strategy` | --proxy-strategy |  |
| `--range` | --range |  |
| `--referer` | --referer |  |
| `-J / --remote-header-name` | --output-name header |  |
| `-O / --remote-name` | ghola download URL or -o DIR/ |  |
| `-R / --remote-time` | --output-remote-time |  |
| `-X / --request` | -m / --method |  |
| `-r / --retry N` | --retry-max N | The default is now 3 for idempotent methods. |
| `--retry-http` | (default) | --retry-status sets the codes. |
| `--serve` | ghola serve | There is no alias. |
| `-s / --silent` | -q / --quiet | v1 -q still writes errors (as curl -sS). |
| `-S / --snoop` | ghola inspect URL | The job is a word. |
| `--speed-limit` | --stall-rate RATE |  |
| `--speed-time` | --stall-time DURATION |  |
| `--stealth-headers` | --impersonate-headers |  |
| `--timeout MS` | --timeout DURATION | The value has a unit: 30s and not 30000. A bare number is seconds. |
| `--timing` | --output-template or -v | -v writes the timing to stderr. |
| `-T / --upload-file` | --upload |  |
| `-u / --user` | -u / --user |  |
| `-A / --user-agent` | --user-agent | No short letter (1.3% of curl files). |
| `-v / --verbose` | -v / --verbose |  |
| `-V / --version` | -V / --version |  |
| `-w / --wget` | ghola download URL | ghola wget runs wget syntax. |
<!-- END v0-to-v1 -->

## 10. Task test

Plan step 5. 44 common tasks, each with the native command and the shortest command that each other tool needs. A blank cell means the tool cannot do the task alone.

**The rule in this draft.**
1. The native command must tie or win against each other tool, on the number of options and on the number of characters.
2. The job word counts as part of the program name, as `wget` and `aria2c` are programs whose default job is to save a file.
3. A task that loses needs a note that starts with "Accepted:" and gives the reason.

The plan's rule counts the job word as an option. No design can meet it, for two reasons. First, `curl URL` prints the page and `wget URL` saves it, and one default cannot tie both. Second, every download or mirror task then loses to `wget` or `aria2c` by the job word alone. Section 11, question D, decides the rule.

"New names" lists the native long names that no other tool has. A user must read the help for those.

<!-- BEGIN tasks -->
| # | Task | Native | Options | Chars | Others (options/chars) | Pass | New names | Note |
|---|---|---|---|---|---|---|---|---|
| 1 | Print a page | `ghola URL` | 0 | 4 | curl 0/4, wget 2/9 | yes |  |  |
| 2 | Print a page in a script: quiet; follow redirects; fail on HTTP errors | `ghola -q URL` | 1 | 7 | curl 4/10, wget 2/9 | yes |  |  |
| 3 | Download a file under its remote name | `ghola download URL` | 0 | 4 | curl 2/8, wget 0/4, aria2 0/4 | yes |  |  |
| 4 | Download a file to a given name | `ghola -o out.zip URL` | 1 | 15 | curl 2/16, wget 1/15, aria2 1/15 | yes |  |  |
| 5 | Download a file into a folder | `ghola -o dl/ URL` | 1 | 11 | curl 3/26, wget 1/10, aria2 1/10 | **no** |  | Accepted: -o DIR/ needs the slash when the folder does not exist yet. One character more than wget -P. |
| 6 | Resume an interrupted download | `ghola download -c URL` | 1 | 7 | curl 3/13, wget 1/7, aria2 1/7 | yes |  |  |
| 7 | Download one file on 8 connections | `ghola download -n8 URL` | 1 | 8 | aria2 2/12 | yes |  |  |
| 8 | Download the URLs in a list; 4 at a time | `ghola download -i urls.txt -j4` | 2 | 16 | aria2 2/16 | yes |  |  |
| 9 | Download the URLs in a list; one at a time | `ghola download -i urls.txt` | 1 | 12 | wget 1/12, aria2 2/16 | yes |  |  |
| 10 | Post JSON with a bearer token | `ghola --json '{"a":1}' --auth-bearer T URL` | 2 | 37 | curl 2/39, wget 5/108 | yes | auth-bearer |  |
| 11 | Post a form field | `ghola -d 'a=1' URL` | 1 | 13 | curl 1/13, wget 3/27 | yes |  |  |
| 12 | Post a URL-encoded field with spaces | `ghola --field 'q=a b' URL` | 1 | 20 | curl 1/29 | yes | field |  |
| 13 | Post the body from a file | `ghola -d @body.txt URL` | 1 | 17 | curl 1/28, wget 3/30 | yes |  |  |
| 14 | Upload a file as a multipart form | `ghola --form file=@x.bin URL` | 1 | 23 | curl 1/19 | **no** |  | Accepted: --form is in 1.4% of curl files. Pasted commands use ghola curl. |
| 15 | Upload a file with PUT | `ghola --upload x.bin URL` | 1 | 19 | curl 1/13, wget 4/40 | **no** | upload | Accepted: --upload-file is in 0.3% of curl files. |
| 16 | Send a custom header | `ghola -H 'X-A: 1' URL` | 1 | 16 | curl 1/16, wget 3/27, aria2 1/22 | yes |  |  |
| 17 | Send Basic credentials | `ghola -u u:p URL` | 1 | 11 | curl 1/11, wget 4/31, aria2 2/34 | yes |  |  |
| 18 | Send a cookie | `ghola --cookie a=1 URL` | 1 | 17 | curl 1/11, wget 3/32, aria2 1/27 | **no** |  | Accepted: --cookie is in 0.8% of curl script files. The 27% in browser-header files go through ghola curl. |
| 19 | Keep cookies in a jar across runs | `ghola --cookie-jar j.txt URL` | 1 | 23 | curl 2/22, wget 5/74, aria2 2/46 | **no** |  | Accepted: one option against two, one character more. |
| 20 | Show only the response headers | `ghola -I URL` | 1 | 7 | curl 1/7, wget 2/16 | yes |  |  |
| 21 | Show the response headers and the body | `ghola --show-headers URL` | 1 | 19 | curl 1/7, wget 3/12 | **no** |  | Accepted: --show-headers is in 0.9% of curl files. |
| 22 | Debug a request | `ghola -v URL` | 1 | 7 | curl 1/7, wget 1/7, aria2 1/30 | yes |  |  |
| 23 | Print only the status code | `ghola -qo /dev/null -w '%{http_code}' URL` | 3 | 36 | curl 3/36 | yes |  |  |
| 24 | Get one field of a JSON answer | `ghola --output-select name URL` | 1 | 25 | none | yes | output-select |  |
| 25 | Skip the TLS certificate check | `ghola -k URL` | 1 | 7 | curl 1/7, wget 3/32, aria2 1/30 | yes |  |  |
| 26 | Trust a private CA | `ghola --tls-ca ca.pem URL` | 1 | 20 | curl 1/20, wget 3/33, aria2 1/28 | yes | tls-ca |  |
| 27 | Send through an HTTP proxy | `ghola --proxy http://p:8080 URL` | 1 | 26 | curl 1/21, wget 4/54, aria2 1/30 | **no** |  | Accepted: --proxy is in 0.3% of curl script files. |
| 28 | Send through SOCKS5 with remote DNS | `ghola --proxy socks5h://p:1080 URL` | 1 | 29 | curl 1/24 | **no** |  | Accepted: as task 27. |
| 29 | Stop after 30 seconds in total | `ghola -t 30 URL` | 1 | 10 | curl 1/10 | yes |  |  |
| 30 | Retry up to 5 times | `ghola -r 5 URL` | 1 | 9 | curl 1/14, wget 3/14, aria2 1/9 | yes |  |  |
| 31 | Retry transient errors | `ghola URL` | 0 | 4 | curl 1/14, wget 2/9, aria2 0/4 | yes |  |  |
| 32 | Limit the speed to 1 MB/s | `ghola --rate-max 1M URL` | 1 | 18 | curl 1/20, wget 3/25, aria2 1/28 | yes | rate-max |  |
| 33 | Force IPv4 | `ghola --ip 4 URL` | 1 | 11 | curl 1/7, wget 3/12, aria2 1/19 | **no** | ip | Accepted: --ipv4 is in 0.1% of curl files. |
| 34 | Connect a host name to a given address | `ghola --resolve example.com:443:127.0.0.1 URL` | 1 | 40 | curl 1/40 | yes |  |  |
| 35 | Use HTTP/2 without an upgrade | `ghola --http 2-prior URL` | 1 | 19 | curl 1/28 | yes | http |  |
| 36 | Request a byte range | `ghola --range 0-99 URL` | 1 | 17 | curl 1/12 | **no** |  | Accepted: --range is in 0.3% of curl files. |
| 37 | Save the response headers to a file | `ghola --save-headers h.txt URL` | 1 | 25 | curl 1/13 | **no** |  | Accepted: --dump-header is in 0.5% of curl files. |
| 38 | Copy Chrome's TLS fingerprint | `ghola --impersonate chrome URL` | 1 | 25 | none | yes | impersonate |  |
| 39 | Download a file and check its SHA-256 | `ghola download --checksum sha-256=ab12 URL` | 1 | 28 | aria2 1/28 | yes |  |  |
| 40 | Download one file from two mirrors | `ghola download URL --source URL2` | 1 | 18 | aria2 0/9 | **no** | source | Accepted: aria2c reads several URLs on one line as mirrors of one file. In ghola several URLs are several files, as in curl and wget. |
| 41 | Mirror a site to depth 2 | `ghola mirror --depth 2 URL` | 1 | 14 | wget 2/12 | **no** | depth | Accepted: one option against two, one character more. |
| 42 | Mirror a site for offline use | `ghola mirror --offline URL` | 1 | 14 | wget 4/16 | yes | offline |  |
| 43 | Check the links of a site and save nothing | `ghola mirror --spider URL` | 1 | 13 | wget 2/16 | yes |  |  |
| 44 | Download in the background with a log | `ghola download --background --log dl.log URL` | 2 | 30 | wget 2/17, aria2 2/17 | **no** |  | Accepted: --background is in 0.5% of wget files. |

30 of 44 tasks pass (22 with the job word counted as an option). 12 tasks use a name that no other tool has.
<!-- END tasks -->

## 11. Decisions

**A. `--ghost` and the bridge field `ghost`.** v0 `--ghost` adds an `X-Ghola-Identity` header, a SHA-256 of the time and the URL (`internal/client/client.go`, `applyGhostSign`). The header marks every request as coming from ghola. That works against impersonation and stealth. scope (`scope/src/http/ghola.rs`) and scope-bca both send `"ghost": true` in their stealth mode, so their stealth requests carry this header today.
Decision (Jon, 2026-11-21): A.

- A. Remove `--ghost`. The bridge accepts the `ghost` field and ignores it, so the clients keep working, and their stealth requests stop sending the header.
- B. Keep it as `--ghost`.

**B. `--jq`.** It takes a gjson path, not jq syntax, so the name misleads.
Decision (Jon, 2026-11-21): A.

- A. Rename it to `--output-select` (this draft).
- B. Keep `--jq`.

**C. Size units.** v0 reads `k` as 1000. curl, wget and aria2 read it as 1024.
Decision (Jon, 2026-11-21): A.

- A. `K`, `M` and `G` are 1024, as in the three tools. `kB`, `MB` and `GB` are 1000, and `KiB`, `MiB` and `GiB` are 1024.
- B. Everything is 1000, as in v0.

**D. The task-test rule.**
Decision (Jon, 2026-11-21): A.

- A. The job word counts as part of the program, and each loss has a recorded reason (this draft). The result: 30 of 44 tasks pass, and 14 have a reason.
- B. The plan's rule: the job word counts as an option. The result: 22 of 44 pass, and no design can pass them all.

**E. `-c`, `-i` and `-w` change their meaning from v0.** In v0 they are `--chain`, `--include` and `--wget`. In v1 they are `--continue`, `--input` and `--output-template`. A v0 command that uses them gets no v0 message. It fails as a different error, or, with `-c URL`, it resumes.
Decision (Jon, 2026-11-21): A.

- A. Keep the new meanings. They come from measured use.
- B. Use other letters, so that each v0 use gets the v0 message.

**F. Mirror depth.** wget `-r` stops at depth 5, and wget `-m` has no limit.
Decision (Jon, 2026-11-21): A.

- A. 5, as in wget `-r` (this draft).
- B. No limit, as in wget `-m`.

**G. Mirror above the start folder.** wget follows links to the parent folders unless `-np` is given.
Decision (Jon, 2026-11-21): A.

- A. Stay below the start folder: `--parent` is off (this draft).
- B. Follow parent links, as in wget.

**H. An existing file in `ghola download` and `ghola mirror`.** curl replaces it. wget and aria2c write `name.1`.
Decision (Jon, 2026-11-21): A.

- A. Stop with exit 23 and name `--output-overwrite` (this draft).
- B. Write `name.1`, as in wget and aria2c.
- C. Replace it, as in curl.

**I. `--variable` expansion.** curl expands `{{name}}` only in an `--expand-NAME` form of an option, because a body that holds `{{` must not change without a sign.

Decision (Jon, 2026-11-21): use curl's rule. `--expand-NAME VALUE` expands the variables in VALUE and then works as `--NAME VALUE`. Every option that takes a value has this form. Other values do not change.

**J. `--output-events` lines.** Decision (Jon, 2026-11-21): one JSON object on each line of stderr, with the events `start`, `retry`, `done` and `error`. `docs/cli/output-events.schema.json` defines the fields.

**K. `--alt-svc`.** Decision (Jon, 2026-11-21): build the cache in v1.0.0. ghola reads the file, records each `Alt-Svc` header, and writes the file in curl's format. An `h3` entry turns on HTTP/3 for that host only on the stack that has HTTP/3 (`--impersonate` with a profile that supports it). The default stack records the entries and does not use them.

**L. BitTorrent (plan D5-B).** Decision (Jon, 2026-11-21): not in v1.0.0. Reason: it needs a large library (for example anacrolix/torrent), and it makes ghola a public peer that uploads to other hosts. That needs its own security review. `--seed-ratio` leaves the v1 table.

**M. A WebSocket transport for the JSON-RPC endpoint.** Decision (Jon, 2026-11-21): add it in v1.0.0 with `github.com/fasthttp/websocket`. `/jsonrpc` takes HTTP POST and WebSocket, with the same Host and token checks.
