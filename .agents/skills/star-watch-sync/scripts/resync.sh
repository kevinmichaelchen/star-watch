#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-resync}"
DEFAULT_STAR_LIST_ID="UL_kwDOAE5HCs4ASM_J"

if [[ ! -f "go.mod" ]] || ! grep -q "module github.com/kevinmichaelchen/star-watch" go.mod; then
  echo "Run this script from the star-watch repo root."
  exit 1
fi

if [[ -z "${STAR_LIST_IDS:-}" && -z "${STAR_LIST_ID:-}" ]]; then
  export STAR_LIST_ID="$DEFAULT_STAR_LIST_ID"
fi

if [[ -z "${GITHUB_TOKEN:-}" ]] && command -v gh >/dev/null 2>&1; then
  token="$(gh auth token 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    export GITHUB_TOKEN="$token"
  fi
fi

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "GITHUB_TOKEN is not set and gh auth token was unavailable."
  exit 1
fi

run_stats() {
  go run ./cmd/star-watch stats
}

case "$MODE" in
  sync)
    go run ./cmd/star-watch sync
    run_stats
    ;;
  refresh-only)
    go run ./cmd/star-watch sync --refresh --skip-enrich
    run_stats
    ;;
  resync)
    if ! go run ./cmd/star-watch sync --refresh; then
      echo "Refresh pass failed; running cache-backed sync to complete processing..."
    fi
    go run ./cmd/star-watch sync
    run_stats
    ;;
  *)
    echo "Unknown mode: $MODE"
    echo "Usage: $0 [sync|refresh-only|resync]"
    exit 1
    ;;
esac
