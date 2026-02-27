---
name: star-watch-sync
description: Run reliable sync and re-sync workflows for star-watch, including refresh-first retries, cache-backed completion, and post-run verification.
---

# Star Watch Sync

Use this skill when the user asks to sync, re-sync, refresh starred repos, or verify sync completion.

## Defaults

- Run commands from the repository root.
- Prefer this star list unless the user overrides it:
  - `STAR_LIST_ID=UL_kwDOAE5HCs4ASM_J`
- Prefer `gh auth token` if `GITHUB_TOKEN` is missing.

## Workflows

### Incremental sync

Use when user asks to "sync" and does not require a forced refresh:

```bash
skills/star-watch-sync/scripts/resync.sh sync
```

### Full re-sync (refresh + completion pass)

Use when user asks to "re-sync" or wants latest stars guaranteed:

```bash
skills/star-watch-sync/scripts/resync.sh resync
```

This runs:
1. `sync --refresh` (refresh cache from GitHub)
2. `sync` (cache-backed completion pass for upsert/enrich/embed)

### Refresh-only sync

Use when user wants cache refresh without enrichment:

```bash
skills/star-watch-sync/scripts/resync.sh refresh-only
```

## Verification

After any sync workflow, verify with:

```bash
go run ./cmd/star-watch stats
```

Interpretation:
- `Repos` should match latest cache count.
- `Enriched == Repos` and `Embedded == Repos` means full processing completed.

## Known failure handling

- `401 Bad credentials`: refresh token source (`gh auth token` or `GITHUB_TOKEN`).
- `Could not resolve to a node with the global id of ''`: missing `STAR_LIST_ID`.
- `context deadline exceeded` during refresh upsert: run follow-up `sync` from refreshed cache.
