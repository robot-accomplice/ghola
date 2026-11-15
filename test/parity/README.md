# Parity harness

The harness runs ghola, curl, wget (or Wget2) and aria2 on the same jobs against a local test server. It checks each result against one rule for all tools. Its job is to measure the claim "ghola does each job at least as well as curl, wget and aria2". See `docs/plans/2026-11-15-parity-and-ux-plan.md`.

## Run it

```bash
go test -tags parity ./test/parity/ -v
```

The test builds ghola from this checkout. It finds the reference tools on `PATH`; on Windows it also looks in the winget package folder. A tool that is not found is skipped, and the summary shows `-` for it.

| Variable | Effect |
|---|---|
| `PARITY_GHOLA` | Use this ghola binary instead of building one. |
| `PARITY_CURL`, `PARITY_WGET`, `PARITY_ARIA2` | Use this reference binary. |
| `PARITY_OUT` | Write the records here. The default is `test/parity/out` (ignored by git). |
| `PARITY_ONLY` | Run only these scenario IDs, comma-separated. |

A full run takes about 2 minutes. The timeout and rate-limit scenarios wait on purpose.

## What it records

For each scenario and tool, `results.json` has the arguments, exit code, time, peak memory, stdout length and SHA-256, stderr, every file written, and every request the server got. `runs/<scenario>.<tool>/` keeps the exact stdout and stderr bytes and the work tree. `summary.md` is a pass/fail table.

## The known-failure list

`known.go` lists the scenarios that ghola fails today and the audit finding for each one. The test fails when:

1. ghola fails a scenario that is not in the list (a regression), or
2. ghola passes a scenario that is in the list. Remove the entry in the same change as the fix.

A reference tool that fails a rule is logged, not treated as an error. The rule states the correct result, so a reference tool can fail it too.

## Add a scenario

1. Add a server route in `server.go` if the case needs one. Use `redirect` for redirects, so they stay inside the run's path prefix.
2. Add a `scenario` in `scenarios.go`: an ID, the finding it belongs to (`ok` for behaviour that must not regress), a command for each tool that can do the job, and a `check`.
3. Run the harness. If ghola fails the new scenario, add it to `known.go` with the reason.

Write the check as the correct result, not as the behaviour of one tool. Use `toFile` when the scenario does not test stdout, so a stdout defect cannot hide the result.
