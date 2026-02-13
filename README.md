# star-watch

Upload repos from a GitHub star list into SurrealDB Cloud with AI-generated
summaries, topic categorization, and vector embeddings for semantic similarity
search.

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- A [SurrealDB Cloud](https://surreal.com/cloud) instance (or self-hosted)
- A [GitHub personal access token](https://github.com/settings/tokens) with
  `read:user` scope
- An OpenAI API key (or any OpenAI-compatible provider) for summaries and
  embeddings

## Getting Started

### 1. Clone and install dependencies

```sh
git clone https://github.com/kevinmichaelchen/star-watch.git
cd star-watch
go mod download
```

### 2. Configure environment

Copy the example below into a `.env` file at the project root. **Never commit
this file** — it is already in `.gitignore`.

```env
# SurrealDB
SURREAL_URL=wss://<your-instance>.surreal.cloud/rpc
SURREAL_NS=<namespace>
SURREAL_DB=<database>
SURREAL_USER=<username>
SURREAL_PASS=<password>

# GitHub
GITHUB_TOKEN=ghp_...
STAR_LIST_ID=UL_...          # Node ID of your star list (find via GraphQL Explorer)

# LLM (any OpenAI-compatible API)
LLM_BASE_URL=https://api.openai.com/v1
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4o-mini

# Embeddings (OpenAI)
EMBEDDING_API_KEY=sk-...
EMBEDDING_MODEL=text-embedding-3-small
```

> **Finding your star list ID:** Open the
> [GitHub GraphQL Explorer](https://docs.github.com/en/graphql/overview/explorer),
> run `query { viewer { lists(first:10) { nodes { id name } } } }`, and copy
> the `id` for your list.

### 3. Initialize the database schema

```sh
go run ./cmd/star-watch schema
```

### 4. Fetch and store repos

```sh
# First run — fetches from GitHub API and caches to stars.json
go run ./cmd/star-watch sync --skip-enrich

# Subsequent runs read from stars.json (no API calls)
go run ./cmd/star-watch sync --skip-enrich

# Force re-fetch from GitHub
go run ./cmd/star-watch sync --skip-enrich --refresh
```

### 5. Enrich with AI summaries and embeddings

```sh
# Full pipeline: upsert + summarize + embed
go run ./cmd/star-watch sync

# Re-enrich all repos (even previously enriched ones)
go run ./cmd/star-watch sync --force
```

### 6. Search and explore

```sh
# Semantic similarity search
go run ./cmd/star-watch search "RAG framework"
go run ./cmd/star-watch search -k 5 "vector database for embeddings"

# Stats and category breakdown
go run ./cmd/star-watch stats
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `star-watch schema` | Initialize/update SurrealDB schema |
| `star-watch sync` | Full pipeline: fetch, enrich, embed, store |
| `star-watch sync --skip-enrich` | Fetch and store only (no LLM/embedding calls) |
| `star-watch sync --force` | Re-enrich all repos |
| `star-watch sync --refresh` | Re-fetch from GitHub (bypass `stars.json` cache) |
| `star-watch search "query"` | Vector similarity search (default top 10) |
| `star-watch search -k 5 "query"` | Vector similarity search (top k) |
| `star-watch stats` | Show counts and category breakdown |

## Architecture

```
cmd/star-watch/main.go        CLI (cobra)
internal/
  config/config.go             .env → Config struct
  models/repo.go               Shared types
  github/github.go             GraphQL star list fetcher
  llm/llm.go                   Pluggable LLM summarizer
  embedding/embedding.go       OpenAI embedding client
  surrealdb/surrealdb.go       DB client, schema, upsert, search
  pipeline/pipeline.go         Orchestration with local JSON cache
```

### Pipeline flow

1. **Fetch** — Paginated GraphQL query (100/page) pulls repo metadata + README
   excerpts. Results are cached to `stars.json` to avoid repeat API calls.
2. **Upsert** — Each repo is merged into SurrealDB via `UPSERT ... MERGE`,
   keyed by `full_name`.
3. **Enrich** — 5 concurrent workers call an OpenAI-compatible LLM to generate
   2-3 sentence summaries and 1-3 topic categories per repo.
4. **Embed** — A single batch call to OpenAI generates 1536-dim vectors from
   `"{full_name}: {ai_summary}"`.
5. **Store** — Embeddings are written back to SurrealDB, indexed with HNSW for
   sub-second KNN queries.

### Caching

GitHub star data is cached to `stars.json` after the first fetch. Subsequent
`sync` runs read from this file instead of hitting the API. Use `--refresh` to
force a fresh fetch.

### Pluggable LLM

Summarization works with any OpenAI-compatible API. Swap providers by changing
`LLM_BASE_URL` and `LLM_MODEL` in `.env`:

```env
# Fireworks
LLM_BASE_URL=https://api.fireworks.ai/inference/v1
LLM_MODEL=accounts/fireworks/models/llama-v3p1-70b-instruct

# Together
LLM_BASE_URL=https://api.together.xyz/v1
LLM_MODEL=meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo
```

## Cost Estimate

With OpenAI GPT-4o-mini and text-embedding-3-small for ~276 repos:

- Summaries: ~$0.07
- Embeddings: ~$0.001
- **Total: under $0.10**

## Notes on GitHub Star List API

The `UserList.items` GraphQL connection is **undocumented**. Observed behavior:

- Items are returned oldest-starred first (matching the GitHub UI default).
- No `orderBy` argument or `starredAt` timestamp is exposed.
- Relay pagination (`first`/`after`/`last`/`before`) works normally.

For incremental fetching with timestamps, `User.starredRepositories` (with
`orderBy: {field: STARRED_AT, direction: DESC}`) is the better option, though
it queries all stars rather than a specific list.
