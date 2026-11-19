# Flag use in real commands

Phase 0B, step 2 (`docs/plans/2026-11-15-parity-and-ux-plan.md`, section 5A). Principle 6 gives short letters only to the most used options. This report gives the measured use.

## Method

1. `scripts/flag_corpus.py` runs 13 GitHub code searches (2026-11-19): `curl -`, `wget -` and `aria2c -` in Dockerfiles, shell scripts, Markdown and YAML, and one search for files with curl and Chrome's `sec-ch-ua` header. It keeps up to 200 files for each search and one file for each repository. It got 2,550 files. `docs/cli/flag-corpus.csv` records the repository, path and commit of each file. The files stay out of the repository (`.flag-corpus/`, gitignored).
2. `scripts/flag_usage.py` finds each command, maps each short letter to its long name with `docs/cli/flag-inventory.csv`, and counts files. A file counts once for each option it uses: one sbt script had 76 commands with `curl -s --list-only`, and by command it outweighed 76 other files.
3. A command must have a URL-like argument, so "apt-get install curl wget" does not count.

Files with a command: curl 1,196, curl with browser headers 133, wget 1,007, aria2c 390. Fewer than 1% of option uses are unknown to the inventory (for example `--external-downloader-args`, which is a yt-dlp option on the same line).

A manual check of 4 random files found no parse error.

## Limits

- Code search ranks by relevance, not at random. The sample is not a random sample of all use.
- Copied snippets count once for each repository. For example, the rustup installer line gives `--proto` and `--tlsv1.2` 5.7% and 5.5%.
- The browser-header search also finds scripts that send browser headers. It is not only pasted "Copy as cURL" output.
- This repository's docs have only 3 files with a ghola command. That is too few to count, so the report leaves them out (they are in the CSV, source `repo-docs`).

## Results

The full counts are in `docs/cli/flag-usage.csv`.

### curl, public files

| Option | Short | Share of files |
|---|---|---|
| `--silent` | `-s` | 64.4% |
| `--location` | `-L` | 50.3% |
| `--output` | `-o` | 49.7% |
| `--fail` | `-f` | 47.2% |
| `--show-error` | `-S` | 21.8% |
| `--header` | `-H` | 11.2% |
| `--request` | `-X` | 10.3% |
| `--head` | `-I` | 9.9% |
| `--data` | `-d` | 7.7% |
| `--list-only` | `-l` | 6.4% |
| `--remote-name` | `-O` | 6.0% |
| `--proto` |  | 5.7% |
| `--tlsv1.2` |  | 5.5% |
| `--retry` |  | 3.9% |
| `--no-buffer` | `-N` | 3.4% |
| `--write-out` | `-w` | 3.3% |
| `--max-time` | `-m` | 2.6% |
| `--insecure` | `-k` | 2.4% |
| `--progress-bar` | `-#` | 2.3% |
| `--retry-delay` |  | 2.2% |

### curl, files with browser headers

| Option | Short | Share of files |
|---|---|---|
| `--header` | `-H` | 81.2% |
| `--data-raw` |  | 35.3% |
| `--cookie` | `-b` | 27.1% |
| `--compressed` |  | 24.1% |
| `--request` | `-X` | 16.5% |
| `--silent` | `-s` | 15.0% |
| `--data` | `-d` | 9.8% |
| `--output` | `-o` | 9.0% |
| `--location` | `-L` | 8.3% |
| `--url` |  | 6.8% |
| `--insecure` | `-k` | 6.0% |
| `--write-out` | `-w` | 5.3% |

### wget, public files

| Option | Short | Share of files |
|---|---|---|
| `--output-document` | `-O` | 70.5% |
| `--quiet` | `-q` | 54.2% |
| `--no-check-certificate` |  | 9.7% |
| `--spider` |  | 9.2% |
| `--no-verbose` |  | 8.2% |
| `--tries` | `-t` | 7.7% |
| `--continue` | `-c` | 5.7% |
| `--directory-prefix` | `-P` | 3.3% |
| `--header` |  | 2.5% |
| `--progress` |  | 2.0% |
| `--recursive` | `-r` | 2.0% |
| `--timeout` | `-T` | 1.7% |
| `--timestamping` | `-N` | 1.7% |
| `--mirror` | `-m` | 1.6% |
| `--no-host-directories` |  | 1.6% |

### aria2c, public files

| Option | Short | Share of files |
|---|---|---|
| `--max-connection-per-server` | `-x` | 64.6% |
| `--out` | `-o` | 57.9% |
| `--dir` | `-d` | 51.8% |
| `--split` | `-s` | 49.2% |
| `--continue` | `-c` | 34.6% |
| `--min-split-size` | `-k` | 30.8% |
| `--console-log-level` |  | 25.4% |
| `--file-allocation` |  | 9.2% |
| `--quiet` | `-q` | 8.2% |
| `--allow-overwrite` |  | 7.4% |
| `--summary-interval` |  | 6.9% |
| `--max-tries` | `-m` | 5.9% |

## What this means for the short letters

These are input to the spec (step 4). They are not decisions.

1. Six jobs occur in more than a third of files for at least one tool: output to a file (curl 49.7%, wget 70.5%, aria2c 57.9%), quiet (curl 64.4%, wget 54.2%), follow redirects (curl 50.3%), fail on HTTP errors (curl 47.2%), connections per server (aria2c 64.6%) and the target folder (aria2c 51.8%).
2. The v1 defaults remove the need for two of them: redirects are on (item 1) and failing on HTTP errors is on (item 3). curl's most common cluster, `-fsSL`, becomes no flags at all, except quiet.
3. Request shaping (header, method, body, cookie) is rare in scripts (header 11.2%) but common in browser-header files (header 81.2%, `--data-raw` 35.3%, cookie 27.1%). Pasted commands go through `ghola curl`, so the native letters can favour script use. Header still gets a letter: it is the top option of the browser-header files.
4. Resume (`-c`: wget 5.7%, aria2c 34.6%), retries (curl 3.9%, wget 7.7%) and the TLS check off (curl 2.4%, wget 9.7%) are the next group.
