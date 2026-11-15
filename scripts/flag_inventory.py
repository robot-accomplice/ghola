#!/usr/bin/env python3
"""List every command-line option of ghola, curl, wget/Wget2 and aria2.

The input for the v1 flag design (docs/plans/2026-11-15-parity-and-ux-plan.md,
section 5A, step 1). It reads each tool's own help output, so the list is
exact for the installed versions, and writes one CSV row per option.

Usage:
    python scripts/flag_inventory.py > docs/cli/flag-inventory.csv

The script finds each tool by a fixed name: first in the repository root
(a local ghola build), then on PATH, then in the winget package folder.
A tool that is not found is skipped. The summary goes to stderr.
"""

import csv
import glob
import os
import re
import shutil
import subprocess
import sys

# The tools to list, and the program names to look for, in order.
TOOLS = {
    "ghola": ("ghola",),
    "curl": ("curl",),
    "wget": ("wget2", "wget"),
    "aria2": ("aria2c",),
}
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

FIELDS = ["tool", "version", "short", "long", "arg", "description", "tags"]


def locate(names):
    """Return the path of the first program in names that exists, or None."""
    exe = ".exe" if os.name == "nt" else ""
    winget = os.path.join(os.environ.get("LOCALAPPDATA", ""), "Microsoft", "WinGet", "Packages")
    for name in names:
        local = os.path.join(ROOT, name + exe)
        if os.path.isfile(local):
            return local
        found = shutil.which(name)
        if found:
            return found
        if os.name == "nt":
            for pattern in ("*", os.path.join("*", "*")):
                hits = glob.glob(os.path.join(winget, pattern, name + exe))
                if hits:
                    return hits[0]
    return None


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
ARIA = re.compile(r"^ (?:(-\S), )?(--[A-Za-z0-9][\w-]*)([^\s\w-]\S*)?(?:\s+(\S.*))?$")


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
        arg = (arg or "").lstrip("[").lstrip("=").rstrip("]")
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
    path = {name: locate(names) for name, names in TOOLS.items()}
    for name, p in path.items():
        if p is None:
            print(f"{name}: not found, skipped", file=sys.stderr)

    rows = []
    if path["ghola"]:
        rows += simple("ghola", first_line(run(path["ghola"], "--version")), run(path["ghola"], "--help"), PFLAG)
    if path["curl"]:
        rows += simple("curl", first_line(run(path["curl"], "--version")), run(path["curl"], "--help", "all"), CURL)
    if path["wget"]:
        rows += wget_rows(first_line(run(path["wget"], "--version")), run(path["wget"], "--help"))
    if path["aria2"]:
        rows += aria_rows(first_line(run(path["aria2"], "--version")), run(path["aria2"], "--help=#all"))

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
