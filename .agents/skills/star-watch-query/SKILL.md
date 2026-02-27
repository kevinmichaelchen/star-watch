---
name: star-watch-query
description: Answer analytical questions over synced starred repositories using star-watch search/stats, including top-N ranked markdown tables (repo, stars, created date, URL).
---

# Star Watch Query

Use this skill when the user asks questions like:
- "top repos related to X"
- "rank my starred repos by stars/date/topic"
- "show a prettified table from my starred repos"

## Defaults

- Run commands from the repository root.
- Prefer markdown table output for ranked/report-style prompts.
- When the user asks for "latest" or "today", refresh first:
  - `skills/star-watch-sync/scripts/resync.sh resync`

## Query Workflow

### 1) Retrieve semantic candidates

Use semantic retrieval first, then re-rank:

```bash
go run ./cmd/star-watch search --json --pool 100 -k 10 \
  --fields "full_name,stars,created_at,url,score" \
  --sort "stars desc" \
  "<topic query>"
```

Notes:
- `--pool 100` means "find top 100 by semantic similarity, then sort by requested field(s)".
- Increase `--pool` for broader topics.
- Keep `score` in fields while debugging relevance.

### 2) Render user-facing table

Preferred table columns for ranking prompts:
- `full_name`
- `stars`
- `created_at`
- `url`

For direct CLI markdown output:

```bash
go run ./cmd/star-watch search --markdown --pool 100 -k 10 \
  --fields "full_name,stars,created_at,url" \
  --sort "stars desc" \
  "<topic query>"
```

### 3) Missing `created_at` handling

If `created_at` is blank for many rows, run a refresh sync to repopulate metadata:

```bash
skills/star-watch-sync/scripts/resync.sh resync
```
