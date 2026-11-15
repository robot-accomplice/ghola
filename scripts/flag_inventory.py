#!/usr/bin/env python3
"""List every command-line option of ghola, curl, wget/Wget2 and aria2.

The input for the v1 flag design (docs/plans/2026-11-15-parity-and-ux-plan.md,
section 5A, step 1). It reads each tool's own help output, so the list is
exact for the installed versions, and writes one CSV row per option.

Usage:
    python scripts/flag_inventory.py --ghola ./ghola --curl curl \
        --wget wget2 --aria2 aria2c > inventory.csv

A tool whose path is not given is skipped. The summary goes to stderr.
"""

import argparse
import csv
import re
import subprocess
import sys

FIELDS = ["tool", "version", "short", "long", "arg", "description", "tags"]


def run(path, *args):
    out = subprocess.run([path, *args], capture_output=True, text=True, errors="replace")
    return out.stdout + out.stderr


def first_line(text):
    return text.strip().splitlines()[0].strip() if text.strip() else ""


def parse_indented(lines, start, cont_indent):
    """Join an option's description with its continuation lines."""
    desc = [lines[start][1]]
    i = start + 1
    while i < len(lines) and lines[i][0] >= cont_indent and lines[i][1]:
        desc.append(lines[i][1])
        i += 1
    return " ".join(desc), i


# curl --help all: " -d, --data <data>   HTTP POST data"
CURL = re.compile(r"^\s*(?:(-\S),\s+)?(--[A-Za-z0-9][\w.-]*)(?:\s+<([^>]+)>)?\s{2,}(\S.*)$")
# pflag (ghola): "  -d, --data string   HTTP POST data"
PFLAG = re.compile(r"^\s+(?:(-\S),\s+)?(--[A-Za-z0-9][\w.-]*)(?:\s([a-zA-Z]+))?\s{2,}(\S.*)$")
# wget and Wget2: "  -c  --continue   Resume..." or "  -c,  --continue  ..."
WGET = re.compile(r"^\s{1,8}(?:(-\S),?\s+)?(--[A-Za-z0-9][\w-]*)(?:=(\S+))?(?:\s{2,}(\S.*))?$")
# aria2c --help=#all: " -x, --max-connection-per-server=NUM   ..."
ARIA = re.compile(r"^ (?:(-\S), )?(--[A-Za-z0-9][\w-]*)(\S*)(?:\s+(\S.*))?$")


def simple(tool, version, text, pattern):
    rows = []
    for line in text.splitlines():
        m = pattern.match(line)
        if m:
            short, long_, arg, desc = m.groups()
            rows.append([tool, version, short or "", long_, arg or "", desc.strip(), ""])
    return rows


def wget_rows(version, text):
    raw = text.splitlines()
    rows, i = [], 0
    while i < len(raw):
        m = WGET.match(raw[i])
        if not m:
            i += 1
            continue
        short, long_, arg, desc = m.groups()
        desc_parts = [desc.strip()] if desc else []
        i += 1
        # Continuation lines are indented past the option column.
        while i < len(raw) and raw[i].strip() and not WGET.match(raw[i]) and len(raw[i]) - len(raw[i].lstrip()) >= 20:
            desc_parts.append(raw[i].strip())
            i += 1
        rows.append(["wget", version, short or "", long_, arg or "", " ".join(desc_parts), ""])
    return rows


def aria_rows(version, text):
    raw = text.splitlines()
    rows, i = [], 0
    while i < len(raw):
        m = ARIA.match(raw[i])
        if not m:
            i += 1
            continue
        short, long_, arg, desc = m.groups()
        arg = arg.lstrip("[").lstrip("=").rstrip("]")
        desc_parts, tags = ([desc.strip()] if desc else []), ""
        i += 1
        while i < len(raw) and not ARIA.match(raw[i]):
            s = raw[i].strip()
            if s.startswith("Tags:"):
                tags = s[len("Tags:"):].strip()
            elif s and not s.startswith(("Possible Values:", "Default:")) and not tags:
                desc_parts.append(s)
            i += 1
        rows.append(["aria2", version, short or "", long_, arg or "", " ".join(desc_parts), tags])
    return rows


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for name in ("ghola", "curl", "wget", "aria2"):
        p.add_argument("--" + name, help=f"path to {name}")
    a = p.parse_args()

    rows = []
    if a.ghola:
        rows += simple("ghola", first_line(run(a.ghola, "--version")), run(a.ghola, "--help"), PFLAG)
    if a.curl:
        rows += simple("curl", first_line(run(a.curl, "--version")), run(a.curl, "--help", "all"), CURL)
    if a.wget:
        rows += wget_rows(first_line(run(a.wget, "--version")), run(a.wget, "--help"))
    if a.aria2:
        rows += aria_rows(first_line(run(a.aria2, "--version")), run(a.aria2, "--help=#all"))

    seen, unique = set(), []
    for r in rows:
        key = (r[0], r[3])
        if key not in seen:
            seen.add(key)
            unique.append(r)

    w = csv.writer(sys.stdout, lineterminator="\n")
    w.writerow(FIELDS)
    w.writerows(unique)

    counts = {}
    for r in unique:
        counts[r[0]] = counts.get(r[0], 0) + 1
    print("options per tool: " + ", ".join(f"{k} {v}" for k, v in counts.items()), file=sys.stderr)


if __name__ == "__main__":
    main()
