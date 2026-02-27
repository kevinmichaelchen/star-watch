---
name: commit-authoring
description: Create semi-detailed Conventional Commit messages and commit staged changes using heredoc.
---

# Commit Authoring

Use this skill whenever creating a git commit.

## Requirements

- Use Conventional Commits for the title line.
- Keep commit messages semi-detailed:
  - 1 concise title line
  - 2-5 body bullets with concrete change details
  - Optional footer for breaking changes or issue references
- Use heredoc for every commit message (no single-line `-m` commits).

## Conventional Commit format

```text
type(scope): subject
```

Allowed `type` values:
- `feat`
- `fix`
- `refactor`
- `perf`
- `docs`
- `test`
- `build`
- `ci`
- `chore`

Scope guidance:
- Use a short scope when possible (e.g. `pipeline`, `skills`, `sync`).
- Omit scope only if the change truly spans unrelated areas.

Subject guidance:
- Use imperative mood (e.g. "add", "update", "remove").
- Keep it specific and under ~72 chars.

## Commit message template

```text
type(scope): subject

- Detail 1
- Detail 2
- Detail 3

BREAKING CHANGE: explain impact
Refs: #123
```

## Command pattern (required)

```bash
git commit -F - <<'COMMIT_MSG'
type(scope): subject

- Detail 1
- Detail 2
COMMIT_MSG
```

## Scripted helper

Use this helper when possible:

```bash
.agents/skills/commit-authoring/scripts/commit_with_heredoc.sh \
  <type> <scope-or-> "<subject>" "<detail1>" ["<detail2>" ...]
```

Examples:

```bash
.agents/skills/commit-authoring/scripts/commit_with_heredoc.sh \
  feat skills "add sync and resync skill" \
  "add star-watch-sync skill instructions" \
  "add resync helper script with refresh fallback"

.agents/skills/commit-authoring/scripts/commit_with_heredoc.sh \
  perf pipeline "add sync performance instrumentation" \
  "measure phase durations in pipeline.Run" \
  "report p50/p95 and slowest operations"
```
