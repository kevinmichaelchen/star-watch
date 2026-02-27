#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  commit_with_heredoc.sh <type> <scope-or-> "<subject>" "<detail1>" ["<detail2>" ...]

Examples:
  commit_with_heredoc.sh feat skills "add sync skill" \
    "add SKILL.md instructions" \
    "add helper script for re-sync flow"

  commit_with_heredoc.sh fix pipeline "handle sync timeout fallback" \
    "retry cache-backed sync after refresh timeout"
USAGE
}

if [[ $# -lt 4 ]]; then
  usage
  exit 1
fi

type="$1"
scope="$2"
subject="$3"
shift 3
details=("$@")

case "$type" in
  feat|fix|refactor|perf|docs|test|build|ci|chore) ;;
  *)
    echo "Invalid Conventional Commit type: $type"
    exit 1
    ;;
esac

if [[ ${#details[@]} -eq 0 ]]; then
  echo "Provide at least one detail bullet."
  exit 1
fi

if ! git diff --cached --quiet; then
  :
else
  echo "No staged changes. Stage files before committing."
  exit 1
fi

if [[ "$scope" == "-" ]]; then
  header="$type: $subject"
else
  header="$type($scope): $subject"
fi

{
  printf '%s\n\n' "$header"
  for d in "${details[@]}"; do
    printf -- '- %s\n' "$d"
  done
} | git commit -F -
