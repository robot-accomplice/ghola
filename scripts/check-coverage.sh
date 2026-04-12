#!/usr/bin/env bash
#
# Coverage gate: enforces a per-package minimum floor and a ratchet that
# prevents any package from regressing below its recorded high-water mark.
#
# Usage:
#   scripts/check-coverage.sh              # enforce both gates
#   scripts/check-coverage.sh --update     # re-record baselines after improvement

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASELINE="$REPO_ROOT/.coverage-baseline.json"
MIN_COVERAGE=80

# Packages that legitimately cannot meet the standard floor due to
# integration-level dependencies (e.g. TLS client, real network).
# Format: "package_suffix:floor"
FLOOR_OVERRIDES=(
  "internal/transport:50"
)

update_mode=false
if [[ "${1:-}" == "--update" ]]; then
  update_mode=true
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

cd "$REPO_ROOT"
go test -cover ./... 2>/dev/null \
  | awk '/^ok/ {
      pkg = $2
      for (i=1; i<=NF; i++) {
        if ($i ~ /^coverage:/) {
          gsub(/%/, "", $(i+1))
          print pkg "\t" $(i+1)
        }
      }
    }' > "$tmpdir/current.tsv"

if [[ ! -s "$tmpdir/current.tsv" ]]; then
  echo "ERROR: failed to parse any package coverage"
  exit 1
fi

get_baseline() {
  local pkg="$1"
  if [[ -f "$BASELINE" ]]; then
    awk -F'"' -v p="$pkg" '$2 == p { gsub(/[^0-9.]/, "", $3); print $3 }' "$BASELINE"
  fi
}

# Returns the coverage floor for a package, checking overrides first.
get_floor() {
  local pkg="$1"
  for entry in "${FLOOR_OVERRIDES[@]}"; do
    local suffix="${entry%%:*}"
    local floor="${entry##*:}"
    if [[ "$pkg" == *"$suffix" ]]; then
      echo "$floor"
      return
    fi
  done
  echo "$MIN_COVERAGE"
}

failed=0

echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "  Coverage Gates (floor: ${MIN_COVERAGE}%)"
echo "═══════════════════════════════════════════════════════════════════"
printf "  %-45s %8s %8s  %s\n" "Package" "Current" "Base" "Status"
echo "─────────────────────────────────────────────────────────────────"

while IFS=$'\t' read -r pkg cov; do
  base=$(get_baseline "$pkg")
  base="${base:-0}"
  short="${pkg#github.com/robot-accomplice/ghola/}"

  status="pass"
  pkg_floor=$(get_floor "$pkg")

  below_floor=$(echo "$cov < $pkg_floor" | bc -l)
  if [[ "$below_floor" -eq 1 ]]; then
    status="FAIL (below ${pkg_floor}% floor)"
    failed=1
  fi

  below_base=$(echo "$cov < $base" | bc -l)
  if [[ "$below_base" -eq 1 ]]; then
    delta=$(echo "$base - $cov" | bc -l)
    status="FAIL (regressed -${delta}% from ${base}%)"
    failed=1
  fi

  printf "  %-45s %7s%% %7s%%  %s\n" "$short" "$cov" "$base" "$status"
done < "$tmpdir/current.tsv"

echo "═══════════════════════════════════════════════════════════════════"
echo ""

if [[ "$update_mode" == true ]]; then
  echo "Updating baseline..."
  {
    echo "{"
    total=$(wc -l < "$tmpdir/current.tsv" | tr -d ' ')
    i=0
    sort "$tmpdir/current.tsv" | while IFS=$'\t' read -r pkg cov; do
      i=$((i + 1))
      comma=","
      if [[ $i -eq $total ]]; then comma=""; fi
      printf '  "%s": %s%s\n' "$pkg" "$cov" "$comma"
    done
    echo "}"
  } > "$BASELINE"
  echo "Baseline updated: $BASELINE"
  exit 0
fi

if [[ $failed -ne 0 ]]; then
  echo "Coverage gate FAILED."
  echo ""
  echo "  If coverage legitimately decreased (e.g. removed dead code),"
  echo "  update the baseline:  scripts/check-coverage.sh --update"
  exit 1
fi

echo "All coverage gates passed."
