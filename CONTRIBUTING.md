# Contributing to Ghola

Thank you for considering a contribution to Ghola. This document explains how to set up a development environment, run the test suite, and submit changes.

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- [golangci-lint](https://golangci-lint.run/welcome/install-locally/) (optional, for local linting)

## Getting Started

```bash
git clone https://github.com/robot-accomplice/ghola.git
cd ghola
go build ./cmd/ghola
```

## Running Tests

```bash
# All tests with race detector
go test -v -race ./...

# With coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

The CI pipeline enforces a **90% minimum** coverage threshold.

## Linting

```bash
gofmt -l .
go vet ./...
golangci-lint run
```

## Project Structure

```
cmd/ghola/          Entrypoint (main.go)
internal/config/    CLI flags, Options, validation
internal/client/    HTTP client, retry, drift, ghost
internal/output/    Response rendering, snoop mode
docs/architecture/  C4 and dataflow diagrams
```

## Submitting Changes

1. Fork the repository and create a feature branch.
2. Write tests for any new functionality (maintain 90%+ coverage).
3. Ensure `go vet`, `gofmt`, and all tests pass.
4. Open a pull request against `main` with a clear description of the change.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Exported types and functions require godoc comments.
- Prefer table-driven tests.
- No global mutable state -- pass `*config.Options` explicitly.

## Reporting Bugs

Open an issue at [github.com/robot-accomplice/ghola/issues](https://github.com/robot-accomplice/ghola/issues) with:

- Steps to reproduce
- Expected vs. actual behavior
- Go version and OS
