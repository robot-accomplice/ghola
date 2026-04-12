# Release Notes — v0.4.0

## Architectural Improvements

This release focuses on code quality, testability, and maintainability
following a comprehensive architectural review.

### Highlights

- **Decomposed FetchURL** — the 260-line monolith is now split into
  focused helpers (`newFetchState`, `prepareRequest`, `handleRedirect`),
  eliminating 7 repeated cleanup blocks and making each concern
  independently testable.

- **Structured Options** — the flat 30-field `Options` struct is now
  organized into `OutputOptions`, `StealthOptions`, `ResilienceOptions`,
  and `ProxyOptions` sub-structs for clarity at call sites.

- **Centralized defaults** — 11 magic values (buffer sizes, timeouts,
  backoff, user-agent, etc.) are consolidated in `config/defaults.go`,
  preventing drift between the CLI and bridge sidecar.

- **Test coverage for previously untested packages** — cookies (86.7%),
  profile (95.6%), and proxy (96.0%) now have comprehensive test suites.
  Transport tests expanded to cover proxy, HTTP/3, and Do paths.

- **Cryptographic proxy selection** — `math/rand` replaced with
  `crypto/rand` for unpredictable proxy rotation in stealth contexts.

- **Error handling fixes** — silent error swallowing in `applyDrift` and
  bridge JSON marshaling now properly surface failures.

- **CI improvements** — coverage gate script supports per-package floor
  overrides for packages with integration-level dependencies.

### Downloads

Pre-built binaries are available on the
[Releases](https://github.com/robot-accomplice/ghola/releases) page
and at [roboticus.ai](https://roboticus.ai).

## Operator Notes

- The `Options` struct fields moved into sub-structs. If you import
  `config.Options` directly (e.g. from the bridge API), update field
  access paths: `opts.Silent` → `opts.Output.Silent`,
  `opts.Impersonate` → `opts.Stealth.Impersonate`, etc.
- The bridge JSON wire format (`bridge.Request` / `bridge.Response`) is
  unchanged — this is a Go-side refactor only.
