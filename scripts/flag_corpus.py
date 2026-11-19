#!/usr/bin/env python3
"""Fetch a sample of public files that run curl, wget or aria2c.

The input for the flag-use count (docs/plans/2026-11-15-parity-and-ux-plan.md,
section 5A, step 2). The script runs each query in QUERIES on GitHub code
search, keeps one file for each repository, and downloads each file at the
commit that search returned. scripts/flag_usage.py counts the options.

Usage:
    python scripts/flag_corpus.py [--per-query N]

It needs `gh`, signed in. Code search allows 10 requests a minute, so the
script waits between pages. The files go to .flag-corpus/ (gitignored: they
are other people's code). The manifest, docs/cli/flag-corpus.csv, records
the query, repository, path and commit of each file, so a later run can get
the same bytes.
"""

import argparse
import csv
import json
import os
import subprocess
import sys
import time
import urllib.parse
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(ROOT, ".flag-corpus")
MANIFEST = os.path.join(ROOT, "docs", "cli", "flag-corpus.csv")

KINDS = ("Dockerfile", "Shell", "Markdown", "YAML")
TOOLS = ("curl", "wget", "aria2c")

# Each query has a name (the source group) and a code search string.
QUERIES = [(f"{tool}/{kind}", f'"{tool} -" language:{kind}') for tool in TOOLS for kind in KINDS]
# Pasted browser "Copy as cURL" commands carry Chrome's client hint headers.
QUERIES.append(("copy-as-curl", '"curl" "sec-ch-ua" "-H"'))

PAGE = 100
SEARCH_GAP = 6.5  # seconds between search requests: 10 a minute
FIELDS = ["query", "repo", "path", "commit", "file"]


def search(q, page):
    out = subprocess.run(
        ["gh", "api", "-X", "GET", "search/code", "-f", f"q={q}", "-f", f"per_page={PAGE}", "-f", f"page={page}"],
        capture_output=True, text=True, check=False,
    )
    if out.returncode != 0:
        print(f"search failed: {q} page {page}: {out.stderr.strip()}", file=sys.stderr)
        return []
    return json.loads(out.stdout).get("items", [])


def raw_url(item):
    # html_url is https://github.com/<repo>/blob/<commit>/<path>.
    repo = item["repository"]["full_name"]
    commit = item["html_url"].split("/blob/", 1)[1].split("/", 1)[0]
    return repo, commit, f"https://raw.githubusercontent.com/{repo}/{commit}/{urllib.parse.quote(item['path'])}"


def fetch(url, dest):
    req = urllib.request.Request(url, headers={"User-Agent": "flag-corpus"})
    with urllib.request.urlopen(req, timeout=30) as rsp:
        data = rsp.read()
    with open(dest, "wb") as f:
        f.write(data)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--per-query", type=int, default=200, help="files to keep for each query")
    args = ap.parse_args()
    os.makedirs(CACHE, exist_ok=True)

    rows = []
    seen = set()  # one file for each repository and query group
    for name, q in QUERIES:
        kept = 0
        for page in range(1, 11):  # code search returns at most 1000 results
            items = search(q, page)
            time.sleep(SEARCH_GAP)
            for item in items:
                repo, commit, url = raw_url(item)
                if (name, repo) in seen or kept >= args.per_query:
                    continue
                file = f"{len(rows):05d}"
                try:
                    fetch(url, os.path.join(CACHE, file))
                except OSError as e:
                    print(f"fetch failed: {url}: {e}", file=sys.stderr)
                    continue
                seen.add((name, repo))
                rows.append({"query": name, "repo": repo, "path": item["path"], "commit": commit, "file": file})
                kept += 1
            if kept >= args.per_query or len(items) < PAGE:
                break
        print(f"{name}: {kept} files", file=sys.stderr)

    with open(MANIFEST, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=FIELDS)
        w.writeheader()
        w.writerows(rows)
    print(f"{len(rows)} files, manifest {MANIFEST}", file=sys.stderr)


if __name__ == "__main__":
    main()
