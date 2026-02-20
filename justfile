set dotenv-load := false

mod         := "github.com/robot-accomplice/ghola"
coverage    := "coverage.out"
threshold   := "90"

# ── Listing ──────────────────────────────────────────────────────────────────

# Show available recipes
default:
    @just --list

# ── Go ───────────────────────────────────────────────────────────────────────

# Format all Go source files
fmt:
    gofmt -w .

# Run static analysis (vet + golangci-lint)
lint:
    gofmt -l . | grep . && { echo "gofmt: files above need formatting"; exit 1; } || true
    go vet ./...
    golangci-lint run --config .github/.golangci.yml

# Run unit tests with race detector
test:
    go test -v -race -count=1 ./...

# Run tests and generate coverage report
cover:
    go test -race -coverprofile={{ coverage }} ./...
    go tool cover -func={{ coverage }}

# Open HTML coverage report in browser
cover-html: cover
    go tool cover -html={{ coverage }}

# Assert coverage meets the 90% threshold
cover-check: cover
    #!/usr/bin/env bash
    total=$(go tool cover -func={{ coverage }} | grep ^total | awk '{print $3}' | tr -d '%')
    echo "Total coverage: ${total}%"
    if [ "$(echo "$total < {{ threshold }}" | bc -l)" -eq 1 ]; then
        echo "FAIL: coverage ${total}% is below {{ threshold }}% threshold"
        exit 1
    fi
    echo "PASS: coverage meets threshold"

# Build the ghola binary (native)
build:
    go build -ldflags "-s -w" -o ghola ./cmd/ghola

# Cross-compile for a target (e.g. just build-cross linux amd64)
build-cross goos goarch:
    GOOS={{ goos }} GOARCH={{ goarch }} go build -ldflags "-s -w" -o /dev/null ./cmd/ghola

# Run govulncheck for known vulnerability scanning
vulncheck:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Tidy module dependencies
tidy:
    go mod tidy

# ── Rust (scope) ─────────────────────────────────────────────────────────────

# Build the scope companion
scope-build:
    cargo build --manifest-path scope/Cargo.toml

# Run scope tests
scope-test:
    cargo test --manifest-path scope/Cargo.toml

# Lint scope with clippy
scope-lint:
    cargo clippy --manifest-path scope/Cargo.toml -- -D warnings

# Format scope
scope-fmt:
    cargo fmt --manifest-path scope/Cargo.toml

# Check scope formatting
scope-fmt-check:
    cargo fmt --manifest-path scope/Cargo.toml --check

# ── CI Emulation ─────────────────────────────────────────────────────────────

# Emulate the full GitHub Actions CI pipeline locally
ci-test: _ci-lint _ci-test _ci-build _ci-security
    @echo ""
    @echo "========================================="
    @echo "  CI-TEST: ALL JOBS PASSED"
    @echo "========================================="

# [CI] Lint job — gofmt, go vet, golangci-lint
_ci-lint:
    @echo "── CI: lint ──"
    @just lint

# [CI] Test job — race tests + 90% coverage gate
_ci-test:
    @echo "── CI: test ──"
    @just cover-check

# [CI] Build job — cross-compile matrix (linux/amd64, darwin/arm64, windows/amd64)
_ci-build:
    @echo "── CI: build (linux/amd64) ──"
    @just build-cross linux amd64
    @echo "── CI: build (darwin/arm64) ──"
    @just build-cross darwin arm64
    @echo "── CI: build (windows/amd64) ──"
    @just build-cross windows amd64

# [CI] Security job — govulncheck
_ci-security:
    @echo "── CI: security ──"
    @just vulncheck

# ── Convenience ──────────────────────────────────────────────────────────────

# Run all checks for both Go and Rust
check-all: ci-test scope-lint scope-fmt-check scope-test
    @echo "All Go + Rust checks passed."

# Clean build artifacts
clean:
    rm -f ghola {{ coverage }}
    rm -rf dist/
    cargo clean --manifest-path scope/Cargo.toml 2>/dev/null || true
