#!/usr/bin/env python3
"""Count how often each option of curl, wget, aria2c and ghola is used.

Step 2 of the v1 flag design (docs/plans/2026-11-15-parity-and-ux-plan.md,
section 5A). The input is the corpus that scripts/flag_corpus.py fetched
(manifest docs/cli/flag-corpus.csv, files in .flag-corpus/) and the Markdown
files of this repository. The option names come from
docs/cli/flag-inventory.csv, so a short letter counts under its long name.

Usage:
    python scripts/flag_usage.py > docs/cli/flag-usage.csv

One row for each source, tool and option. The unit is a file, not a
command: one script can hold 70 commands with the same options, and it would
outweigh 70 other files. "files" is the number of files with at least one
command that uses the option; "share" is files divided by all files with a
command of that tool in that source. The summary goes to stderr.

A command is a tool name followed by its arguments, up to a shell operator
(| ; && || > < and the like). Backslash line ends join lines first. A
command must have a URL-like argument, so a package list such as
"apt-get install curl wget" does not count as a command.
"""

import collections
import csv
import glob
import os
import re
import shlex
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(ROOT, ".flag-corpus")
MANIFEST = os.path.join(ROOT, "docs", "cli", "flag-corpus.csv")
INVENTORY = os.path.join(ROOT, "docs", "cli", "flag-inventory.csv")

# Program name -> inventory tool name.
PROGRAMS = {"curl": "curl", "wget": "wget", "wget2": "wget", "aria2c": "aria2", "ghola": "ghola"}

# The Wget2 help output has no argument markers, so the inventory cannot say
# which wget short letters take a value. These are the ones that do (wget
# and Wget2 manuals).
WGET_SHORT_ARGS = set("aABDeiIlLoOPQRtTUwX")

STOP = {"|", "||", "&", "&&", ";", ";;", ">", ">>", "<", "<<", "(", ")", "2>", "2>&1", ">&"}
URLISH = re.compile(r"://|^\$|^[\w.-]+\.[a-z]{2,}(?:[:/]|$)", re.I)

FIELDS = ["source", "tool", "option", "short", "files", "share"]

# wget 1 has two-letter short options that start with "n". Wget2 keeps them.
WGET_N_OPTIONS = {"v": "--no-verbose", "c": "--no-clobber", "d": "--no-directories",
                  "H": "--no-host-directories", "p": "--no-parent"}


def load_inventory():
    """Return {tool: (short -> (long, takes_arg), set of longs)}."""
    shorts = collections.defaultdict(dict)
    longs = collections.defaultdict(set)
    with open(INVENTORY, encoding="utf-8") as f:
        for r in csv.DictReader(f):
            tool, long, short, arg = r["tool"], r["long"], r["short"], r["arg"]
            longs[tool].add(long)
            if short:
                if tool == "wget":
                    takes = short[1:] in WGET_SHORT_ARGS
                else:
                    # aria2 "true|false" values are optional, and -h takes an optional topic.
                    takes = bool(arg) and arg != "true|false" and not (tool == "aria2" and short == "-h")
                shorts[tool][short[1:]] = (long, takes)
    return shorts, longs


def logical_lines(text):
    """Join backslash line ends, as the shell does."""
    return re.sub(r"\\\r?\n", " ", text).splitlines()


def tokens(line):
    lex = shlex.shlex(line, posix=True, punctuation_chars=";&|<>()")
    lex.whitespace_split = True
    lex.commenters = ""
    try:
        return list(lex)
    except ValueError:  # an unclosed quote: fall back to a plain split
        return line.split()


def program(tok):
    name = tok.strip("`'\"$(").rsplit("/", 1)[-1].lower()
    if name.endswith(".exe"):
        name = name[:-4]
    return PROGRAMS.get(name)


def long_option(tool, arg, longs):
    """Return the name of a --long option, marked when the inventory lacks it."""
    name = arg.split("=", 1)[0]
    # The help lists a boolean once; its --no- form is implied.
    known = name in longs[tool] or (name.startswith("--no-") and "--" + name[5:] in longs[tool])
    return name if known else name + " (unknown)"


def short_options(tool, letters, shorts):
    """Return the long names of a short letter group, such as "fsSL", and
    whether the next token is the value of its last letter."""
    if tool == "wget" and len(letters) == 2 and letters[0] == "n" and letters[1] in WGET_N_OPTIONS:
        return {WGET_N_OPTIONS[letters[1]]}, False
    found = set()
    for i, ch in enumerate(letters):
        long, takes = shorts[tool].get(ch, ("-" + ch + " (unknown)", False))
        found.add(long)
        if takes:
            return found, i == len(letters) - 1  # else the value follows the letter
    return found, False


def parse(tool, args, shorts, longs):
    """Return the set of long option names in args, or None if not a command."""
    found = set()
    urlish = False
    takes_value = False
    for a in args:
        if takes_value:
            takes_value = False
        elif a == "--":
            break
        elif a.startswith("--") and len(a) > 2:
            # A long option without "=" may take the next token. The count
            # does not need the value, so the token is not skipped.
            found.add(long_option(tool, a, longs))
        elif a.startswith("-") and "$" in a:
            pass  # a shell variable, such as -$1: not a fixed option
        elif a.startswith("-") and len(a) > 1 and not a[1].isdigit():
            opts, takes_value = short_options(tool, a[1:], shorts)
            found |= opts
        elif URLISH.search(a):
            urlish = True
    return found if urlish else None


def commands(text):
    for line in logical_lines(text):
        toks = tokens(line)
        for i, t in enumerate(toks):
            tool = program(t)
            if not tool:
                continue
            args = []
            for a in toks[i + 1:]:
                if a in STOP:
                    break
                args.append(a)
            yield tool, args


def sources():
    """Yield (source, text) for each corpus file and repository Markdown file."""
    if os.path.isfile(MANIFEST):
        with open(MANIFEST, encoding="utf-8") as f:
            for r in csv.DictReader(f):
                source = "copy-as-curl" if r["query"] == "copy-as-curl" else "github"
                path = os.path.join(CACHE, r["file"])
                if os.path.isfile(path):
                    with open(path, encoding="utf-8", errors="replace") as g:
                        yield source, g.read()
    for path in glob.glob(os.path.join(ROOT, "**", "*.md"), recursive=True):
        if os.sep + "." in os.path.relpath(path, ROOT):
            continue  # .flag-corpus, .github templates and other dot folders
        with open(path, encoding="utf-8", errors="replace") as g:
            yield "repo-docs", g.read()


def main():
    shorts, longs = load_inventory()
    short_of = {(t, v[0]): "-" + k for t, m in shorts.items() for k, v in m.items()}
    totals = collections.Counter()
    counts = collections.Counter()
    for source, text in sources():
        used = collections.defaultdict(set)  # tool -> options used in this file
        for tool, args in commands(text):
            found = parse(tool, args, shorts, longs)
            if found is not None:
                used[tool] |= found
        for tool, opts in used.items():
            totals[(source, tool)] += 1
            for opt in opts:
                counts[(source, tool, opt)] += 1

    w = csv.DictWriter(sys.stdout, fieldnames=FIELDS, lineterminator="\n")
    w.writeheader()
    for (source, tool, opt), n in sorted(counts.items(), key=lambda kv: (kv[0][0], kv[0][1], -kv[1], kv[0][2])):
        w.writerow({
            "source": source, "tool": tool, "option": opt, "short": short_of.get((tool, opt), ""),
            "files": n, "share": f"{n / totals[(source, tool)]:.3f}",
        })
    for (source, tool), n in sorted(totals.items()):
        unknown = sum(c for (s, t, o), c in counts.items() if s == source and t == tool and o.endswith("(unknown)"))
        print(f"{source} {tool}: {n} files, {unknown} unknown option uses", file=sys.stderr)


if __name__ == "__main__":
    main()
