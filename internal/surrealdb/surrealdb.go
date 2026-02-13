package surrealdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kevinmichaelchen/star-watch/internal/config"
	"github.com/kevinmichaelchen/star-watch/internal/models"
	sdk "github.com/surrealdb/surrealdb.go"
)

type Client struct {
	db *sdk.DB
}

func NewClient(ctx context.Context, cfg *config.Config) (*Client, error) {
	db, err := sdk.FromEndpointURLString(ctx, cfg.SurrealURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to SurrealDB: %w", err)
	}

	if _, err := db.SignIn(ctx, sdk.Auth{
		Namespace: cfg.SurrealNS,
		Database:  cfg.SurrealDB,
		Username:  cfg.SurrealUser,
		Password:  cfg.SurrealPass,
	}); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("signing in: %w", err)
	}

	if err := db.Use(ctx, cfg.SurrealNS, cfg.SurrealDB); err != nil {
		_ = db.Close(ctx)
		return nil, fmt.Errorf("selecting ns/db: %w", err)
	}

	return &Client{db: db}, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.db.Close(ctx)
}

func (c *Client) InitSchema(ctx context.Context) error {
	schema := `
DEFINE TABLE IF NOT EXISTS repo SCHEMAFULL;

DEFINE FIELD IF NOT EXISTS owner          ON TABLE repo TYPE string;
DEFINE FIELD IF NOT EXISTS name           ON TABLE repo TYPE string;
DEFINE FIELD IF NOT EXISTS full_name      ON TABLE repo TYPE string;
DEFINE FIELD IF NOT EXISTS description    ON TABLE repo TYPE option<string>;
DEFINE FIELD IF NOT EXISTS url            ON TABLE repo TYPE string;
DEFINE FIELD IF NOT EXISTS homepage_url   ON TABLE repo TYPE option<string>;
DEFINE FIELD IF NOT EXISTS stars          ON TABLE repo TYPE int;
DEFINE FIELD IF NOT EXISTS language       ON TABLE repo TYPE option<string>;
DEFINE FIELD IF NOT EXISTS topics         ON TABLE repo TYPE array<string>;
DEFINE FIELD IF NOT EXISTS readme_excerpt ON TABLE repo TYPE option<string>;
DEFINE FIELD IF NOT EXISTS ai_summary     ON TABLE repo TYPE option<string>;
DEFINE FIELD IF NOT EXISTS ai_categories  ON TABLE repo TYPE option<array<string>>;
DEFINE FIELD IF NOT EXISTS embedding      ON TABLE repo TYPE option<array<float>>;
DEFINE FIELD IF NOT EXISTS fetched_at     ON TABLE repo TYPE datetime;
DEFINE FIELD IF NOT EXISTS enriched_at    ON TABLE repo TYPE option<datetime>;

DEFINE INDEX IF NOT EXISTS idx_full_name ON TABLE repo FIELDS full_name UNIQUE;
REMOVE INDEX IF EXISTS idx_hnsw_embedding ON TABLE repo;
DEFINE INDEX idx_hnsw_embedding ON TABLE repo FIELDS embedding HNSW DIMENSION 768 DIST COSINE;
`
	_, err := sdk.Query[any](ctx, c.db, schema, nil)
	if err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}
	return nil
}

func (c *Client) UpsertRepo(ctx context.Context, r models.Repo) error {
	// Build data map with only non-nil optional fields to avoid
	// CBOR NULL vs SurrealDB NONE mismatch.
	id := strings.ReplaceAll(r.FullName, "/", "__")
	data := map[string]any{
		"owner":      r.Owner,
		"name":       r.Name,
		"full_name":  r.FullName,
		"url":        r.URL,
		"stars":      r.Stars,
		"fetched_at": time.Now().UTC(),
	}
	if r.Description != nil {
		data["description"] = *r.Description
	}
	if r.HomepageURL != nil {
		data["homepage_url"] = *r.HomepageURL
	}
	if r.Language != nil {
		data["language"] = *r.Language
	}
	topics := r.Topics
	if topics == nil {
		topics = []string{}
	}
	data["topics"] = topics
	if r.ReadmeExcerpt != nil {
		data["readme_excerpt"] = *r.ReadmeExcerpt
	}

	_, err := sdk.Query[any](ctx, c.db,
		`UPSERT type::thing("repo", $id) MERGE $data`,
		map[string]any{
			"id":   id,
			"data": data,
		})
	if err != nil {
		return fmt.Errorf("upserting %s: %w", r.FullName, err)
	}
	return nil
}

func (c *Client) GetUnenrichedRepos(ctx context.Context) ([]models.Repo, error) {
	results, err := sdk.Query[[]models.Repo](ctx, c.db,
		`SELECT * FROM repo WHERE ai_summary IS NONE`, nil)
	if err != nil {
		return nil, fmt.Errorf("querying unenriched repos: %w", err)
	}
	if len(*results) == 0 {
		return nil, nil
	}
	return (*results)[0].Result, nil
}

func (c *Client) GetAllRepos(ctx context.Context) ([]models.Repo, error) {
	results, err := sdk.Query[[]models.Repo](ctx, c.db,
		`SELECT * FROM repo`, nil)
	if err != nil {
		return nil, fmt.Errorf("querying all repos: %w", err)
	}
	if len(*results) == 0 {
		return nil, nil
	}
	return (*results)[0].Result, nil
}

func (c *Client) GetReposNeedingEmbedding(ctx context.Context) ([]models.Repo, error) {
	results, err := sdk.Query[[]models.Repo](ctx, c.db,
		`SELECT * FROM repo WHERE ai_summary IS NOT NONE AND embedding IS NONE`, nil)
	if err != nil {
		return nil, fmt.Errorf("querying repos needing embedding: %w", err)
	}
	if len(*results) == 0 {
		return nil, nil
	}
	return (*results)[0].Result, nil
}

func (c *Client) UpdateEnrichment(ctx context.Context, fullName string, summary string, categories []string) error {
	if categories == nil {
		categories = []string{}
	}
	_, err := sdk.Query[any](ctx, c.db,
		`UPDATE repo SET
			ai_summary = $ai_summary,
			ai_categories = $ai_categories,
			enriched_at = time::now()
		WHERE full_name = $full_name`,
		map[string]any{
			"full_name":     fullName,
			"ai_summary":    summary,
			"ai_categories": categories,
		})
	if err != nil {
		return fmt.Errorf("updating enrichment for %s: %w", fullName, err)
	}
	return nil
}

func (c *Client) UpdateEmbedding(ctx context.Context, fullName string, embedding []float32) error {
	_, err := sdk.Query[any](ctx, c.db,
		`UPDATE repo SET embedding = $embedding WHERE full_name = $full_name`,
		map[string]any{
			"full_name": fullName,
			"embedding": embedding,
		})
	if err != nil {
		return fmt.Errorf("updating embedding for %s: %w", fullName, err)
	}
	return nil
}

func (c *Client) VectorSearch(ctx context.Context, queryVec []float32, k int) ([]models.SearchResult, error) {
	// NOTE: The HNSW KNN operator (<|K|>) returns empty results despite the
	// index existing. This appears to be a SurrealDB bug where the HNSW index
	// is not rebuilt after REMOVE INDEX + DEFINE INDEX. Fall back to brute-force
	// cosine similarity which works correctly with 277 repos.
	query := fmt.Sprintf(`
		SELECT full_name, description, ai_summary, ai_categories, stars, url,
			vector::similarity::cosine(embedding, $query_vec) AS score
		FROM repo
		WHERE embedding IS NOT NONE
		ORDER BY score DESC
		LIMIT %d
	`, k)

	results, err := sdk.Query[[]models.SearchResult](ctx, c.db, query,
		map[string]any{"query_vec": queryVec})
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(*results) == 0 {
		return nil, nil
	}
	return (*results)[0].Result, nil
}

type Stats struct {
	Total    int
	Enriched int
	Embedded int
}

func (c *Client) GetStats(ctx context.Context) (*Stats, error) {
	results, err := sdk.Query[[]map[string]any](ctx, c.db,
		`SELECT
			count() AS total,
			math::sum(IF ai_summary IS NOT NONE THEN 1 ELSE 0 END) AS enriched,
			math::sum(IF embedding IS NOT NONE THEN 1 ELSE 0 END) AS embedded
		FROM repo GROUP ALL`,
		nil)
	if err != nil {
		return nil, fmt.Errorf("getting stats: %w", err)
	}
	if len(*results) == 0 || len((*results)[0].Result) == 0 {
		return &Stats{}, nil
	}
	row := (*results)[0].Result[0]
	return &Stats{
		Total:    toInt(row["total"]),
		Enriched: toInt(row["enriched"]),
		Embedded: toInt(row["embedded"]),
	}, nil
}

type CategoryCount struct {
	Category string
	Count    int
}

func (c *Client) GetCategoryBreakdown(ctx context.Context) ([]CategoryCount, error) {
	// Fetch all repos with categories and compute in Go
	results, err := sdk.Query[[]models.Repo](ctx, c.db,
		`SELECT ai_categories FROM repo WHERE ai_categories IS NOT NONE`, nil)
	if err != nil {
		return nil, fmt.Errorf("getting categories: %w", err)
	}
	if len(*results) == 0 {
		return nil, nil
	}
	counts := map[string]int{}
	for _, r := range (*results)[0].Result {
		for _, cat := range r.AICategories {
			counts[cat]++
		}
	}
	var out []CategoryCount
	for cat, cnt := range counts {
		out = append(out, CategoryCount{Category: cat, Count: cnt})
	}
	return out, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	default:
		return 0
	}
}
