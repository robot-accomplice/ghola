#!/usr/bin/env python3
"""Check the v1 option table and run the task test.

Phase 0B, steps 4 and 5 (docs/plans/2026-11-15-parity-and-ux-plan.md,
section 5A). The spec data lives in three CSV files in docs/cli/:

    v1-options.csv  each v1 option: name, letter, type, default, equivalents
    v0-to-v1.csv    each v0 flag and its v1 form
    v1-tasks.csv    common tasks, with the command for each tool

The script checks the data and fails on an error:
    1. Each option name and each short letter is used once.
    2. Each flag that `ghola --help` lists has a row in v0-to-v1.csv.
    3. Each option in a v1 column (mapping or task) exists in v1-options.csv.

Then it measures each task: the number of options and the number of
characters after the program name. The job word (ghola download, ghola
mirror) counts as part of the program, as aria2c and wget are programs
whose default job is a download. The native command passes when it ties
or wins on both counts against every tool that can do the task. A task
that loses needs a note that starts with "Accepted:" and gives the reason;
a note on a task that passes is an error, so an old exception cannot stay.
The strict count, with the job word as an option, is reported too. "new"
lists the native long names that no other tool has: a user must read the
help for those.

It writes the tables into docs/cli-v1.md between the BEGIN and END
markers, and prints the task summary to stderr.

Usage:
    python scripts/cli_spec.py          # check, measure and write
    python scripts/cli_spec.py --check  # check and measure only
"""

import csv
import json
import os
import re
import shlex
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CLI = os.path.join(ROOT, "docs", "cli")
SPEC = os.path.join(ROOT, "docs", "cli-v1.md")
SUBCOMMANDS = {"download", "mirror", "jobs", "serve", "inspect", "explain", "profiles",
               "completion", "curl", "wget", "aria2c"}
TOOLS = ("curl", "wget", "aria2")
GROUP_TITLES = {
    "output": "Output", "request": "Request", "http": "HTTP", "limits": "Time and speed limits",
    "retry": "Retries", "net": "Names and connections", "tls": "TLS", "auth": "Authentication",
    "proxy": "Proxies", "cookie": "Cookies", "impersonate": "Impersonation",
    "download": "Download manager", "mirror": "Mirror", "general": "General",
}


def read(name):
    with open(os.path.join(CLI, name), encoding="utf-8", newline="") as f:
        return list(csv.DictReader(f))


class Options:
    def __init__(self, rows):
        self.rows = rows
        self.by_name = {r["name"]: r for r in rows}
        self.by_short = {r["short"][1:]: r for r in rows if r["short"]}

    def errors(self):
        errs = []
        names = [r["name"] for r in self.rows]
        shorts = [r["short"] for r in self.rows if r["short"]]
        errs += [f"option name used twice: {n}" for n in sorted({n for n in names if names.count(n) > 1})]
        errs += [f"short letter used twice: {s}" for s in sorted({s for s in shorts if shorts.count(s) > 1})]
        errs += [f"short is not one letter: {s}" for s in shorts if not re.fullmatch(r"-[A-Za-z]", s)]
        errs += [f"unknown group {r['group']} for {r['name']}" for r in self.rows if r["group"] not in GROUP_TITLES]
        return errs

    def long_exists(self, name):
        if name in self.by_name:
            return True
        if name.startswith("no-") and self.by_name.get(name[3:], {}).get("type") == "bool":
            return True
        return name.startswith("proxy-tls-") and name[len("proxy-"):] in self.by_name

    def check_tokens(self, tokens):
        """Return the unknown options in a native command (tokens after the program)."""
        bad = []
        for t in tokens:
            if t.startswith("--"):
                if not self.long_exists(t[2:].split("=", 1)[0]):
                    bad.append(t)
            elif re.match(r"-[A-Za-z]", t):
                # A letter group, or one letter with its value attached (-n8).
                for ch in t[1:]:
                    if ch not in self.by_short:
                        bad.append(f"-{ch}")
                        break
                    if self.by_short[ch]["type"] != "bool":
                        break
        return bad


def option_tokens(cmd):
    """Return the tokens after the program, and the number of options in them."""
    toks = shlex.split(cmd)[1:]
    n = 1 if toks and toks[0] in SUBCOMMANDS else 0
    for t in toks:
        if t.startswith("--"):
            n += 1
        elif t.startswith("-") and t != "-":
            # -fsSL is four options; -n8 and -qO- carry a value after the letters.
            n += max(1, len(re.match(r"-([A-Za-z]*)", t).group(1)))
    return toks, n


def ghola_v0_flags():
    out = subprocess.run(["go", "run", "./cmd/ghola", "--help"], cwd=ROOT, capture_output=True, text=True)
    text = out.stdout + out.stderr
    return sorted(set(re.findall(r"^\s+(?:-[A-Za-z], )?(--[a-z0-9-]+)", text, re.M)))


def check_mapping(opts, mapping):
    errs = []
    v0 = ghola_v0_flags()
    if not v0:
        errs.append("ghola --help listed no flags")
    mapped = set(re.findall(r"--[a-z0-9-]+", " ".join(r["v0"] for r in mapping)))
    errs += [f"v0 flag without a v1 mapping: {f}" for f in v0 if f not in mapped]
    for r in mapping:
        for t in re.findall(r"(?<![\w-])--?[a-z][a-z0-9-]*", r["v1"]):
            if opts.check_tokens([t]):
                errs.append(f"v0-to-v1 names an unknown v1 option: {t} ({r['v0']})")
    return errs, len(v0)


def all_long_names(opts):
    """Long option names of curl, wget and aria2, from the flag inventory."""
    names = set()
    for r in read("flag-inventory.csv"):
        if r["tool"] != "ghola":
            names.add(r["long"].lstrip("-"))
    return names


def measure(opts, tasks):
    other_names = all_long_names(opts)
    results, errs = [], []
    for t in tasks:
        toks, strict_n = option_tokens(t["native"])
        errs += [f"task {t['id']}: unknown native option {b}" for b in opts.check_tokens(toks)]
        strict_chars = len(t["native"]) - len("ghola")
        job = toks[0] if toks and toks[0] in SUBCOMMANDS else ""
        n = strict_n - (1 if job else 0)
        chars = strict_chars - (len(job) + 1 if job else 0)
        new = sorted({x[2:].split("=", 1)[0] for x in toks if x.startswith("--")} - other_names)
        others = {}
        for tool in TOOLS:
            cmd = t[tool]
            if cmd:
                prog = cmd.split()[0]
                others[tool] = (option_tokens(cmd)[1], len(cmd) - len(prog))
        best_n = min((v[0] for v in others.values()), default=n)
        best_c = min((v[1] for v in others.values()), default=chars)
        won = n <= best_n and chars <= best_c
        accepted = t["note"].startswith("Accepted:")
        if won and accepted:
            errs.append(f"task {t['id']} passes but has an Accepted note: remove the note")
        if not won and not accepted:
            errs.append(f"task {t['id']} loses (native {n}/{chars}, others {others}) and has no Accepted note")
        results.append({**t, "n": n, "chars": chars, "new": new, "others": others, "pass": won,
                        "strict": strict_n <= best_n and strict_chars <= best_c})
    return results, errs


def md_escape(s):
    return s.replace("|", "\\|")


def options_tables(opts):
    out = []
    for group, title in GROUP_TITLES.items():
        rows = [r for r in opts.rows if r["group"] == group]
        if not rows:
            continue
        out += [f"### {title}", "", "| Option | Type | Default | curl | wget | aria2 | Phase | Description |",
                "|---|---|---|---|---|---|---|---|"]
        for r in rows:
            name = f"`--{r['name']}`" + (f", `{r['short']}`" if r["short"] else "")
            if r["scope"] != "all":
                name += f" ({r['scope']})"
            cells = [name] + [md_escape(r[k]) for k in ("type", "default", "curl", "wget", "aria2", "phase", "description")]
            out.append("| " + " | ".join(cells) + " |")
        out.append("")
    return out


def mapping_table(mapping):
    out = ["| v0 | v1 | Note |", "|---|---|---|"]
    out += [f"| `{md_escape(r['v0'])}` | {md_escape(r['v1'])} | {md_escape(r['note'])} |" for r in mapping]
    return out + [""]


def tasks_table(results):
    out = ["| # | Task | Native | Options | Chars | Others (options/chars) | Pass | New names | Note |",
           "|---|---|---|---|---|---|---|---|---|"]
    for r in results:
        best = ", ".join(f"{k} {v[0]}/{v[1]}" for k, v in r["others"].items()) or "none"
        out.append(f"| {r['id']} | {md_escape(r['task'])} | `{md_escape(r['native'])}` | {r['n']} | {r['chars']} "
                   f"| {best} | {'yes' if r['pass'] else '**no**'} | {', '.join(r['new'])} | {md_escape(r['note'])} |")
    passed = sum(r["pass"] for r in results)
    strict = sum(r["strict"] for r in results)
    out += ["", f"{passed} of {len(results)} tasks pass ({strict} with the job word counted as an option). "
            f"{sum(1 for r in results if r['new'])} tasks use a name that no other tool has.", ""]
    return out


def write_section(text, marker, lines):
    begin, end = f"<!-- BEGIN {marker} -->", f"<!-- END {marker} -->"
    i, j = text.index(begin) + len(begin), text.index(end)
    return text[:i] + "\n" + "\n".join(lines) + text[j:]


GO_TABLE = os.path.join(ROOT, "internal", "cli", "table_gen.go")


def go_str(v):
    """A Go string literal. JSON string escapes are valid Go escapes."""
    return json.dumps(v, ensure_ascii=False)


def go_table(opts, mapping):
    """The Go source of the option table and the v0 mapping."""
    out = [
        "// Code generated by scripts/cli_spec.py from docs/cli/v1-options.csv",
        "// and docs/cli/v0-to-v1.csv. DO NOT EDIT.",
        "",
        "package cli",
        "",
        "var table = []Option{",
    ]
    for r in opts.rows:
        short = repr(r["short"][1]) if r["short"] else "0"
        fields = [
            f"Name: {go_str(r['name'])}", f"Short: {short}", f"Group: {go_str(r['group'])}",
            f"Scope: {go_str(r['scope'])}", f"Type: {go_str(r['type'])}", f"Default: {go_str(r['default'])}",
            f"Curl: {go_str(r['curl'])}", f"Wget: {go_str(r['wget'])}", f"Aria2: {go_str(r['aria2'])}",
            f"Phase: {go_str(r['phase'])}", f"Help: {go_str(r['description'])}",
        ]
        out.append("\t{" + ", ".join(fields) + "},")
    out += ["}", "", "var v0Table = []V0Flag{"]
    for r in mapping:
        out.append(f"\t{{V0: {go_str(r['v0'])}, V1: {go_str(r['v1'])}, Note: {go_str(r['note'])}}},")
    out += ["}", ""]
    return "\n".join(out)


def main():
    opts = Options(read("v1-options.csv"))
    mapping = read("v0-to-v1.csv")
    errs = opts.errors()
    map_errs, n_v0 = check_mapping(opts, mapping)
    errs += map_errs
    results, task_errs = measure(opts, read("v1-tasks.csv"))
    errs += task_errs
    for e in errs:
        print(f"error: {e}", file=sys.stderr)
    passed = sum(r["pass"] for r in results)
    strict = sum(r["strict"] for r in results)
    print(f"strict (job word as an option): {strict} of {len(results)}", file=sys.stderr)
    print(f"{len(opts.rows)} options, {len(opts.by_short)} short letters, {n_v0} v0 flags mapped, "
          f"{passed} of {len(results)} tasks pass", file=sys.stderr)
    if errs:
        sys.exit(1)
    if "--check" in sys.argv:
        return
    os.makedirs(os.path.dirname(GO_TABLE), exist_ok=True)
    with open(GO_TABLE, "w", encoding="utf-8", newline="\n") as f:
        f.write(go_table(opts, mapping))
    with open(SPEC, encoding="utf-8", newline="") as f:
        text = f.read()
    nl = "\r\n" if "\r\n" in text else "\n"
    text = text.replace("\r\n", "\n")
    text = write_section(text, "options", options_tables(opts))
    text = write_section(text, "v0-to-v1", mapping_table(mapping))
    text = write_section(text, "tasks", tasks_table(results))
    with open(SPEC, "w", encoding="utf-8", newline="") as f:
        f.write(text.replace("\n", nl))


if __name__ == "__main__":
    main()
