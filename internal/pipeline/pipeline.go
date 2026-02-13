package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/kevinmichaelchen/star-watch/internal/config"
	"github.com/kevinmichaelchen/star-watch/internal/embedding"
	"github.com/kevinmichaelchen/star-watch/internal/github"
	"github.com/kevinmichaelchen/star-watch/internal/llm"
	"github.com/kevinmichaelchen/star-watch/internal/models"
	"github.com/kevinmichaelchen/star-watch/internal/surrealdb"
	"golang.org/x/sync/errgroup"
)

const cacheFile = "stars.json"

type Options struct {
	SkipEnrich bool
	Force      bool
	Refresh    bool
}

func Run(ctx context.Context, cfg *config.Config, opts Options) error {
	// Connect to SurrealDB
	fmt.Println("Connecting to SurrealDB...")
	db, err := surrealdb.NewClient(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(ctx) }()

	// Ensure schema
	if err := db.InitSchema(ctx); err != nil {
		return err
	}

	// Step 1: Load repos (from cache or GitHub)
	repos, err := loadRepos(ctx, cfg, opts.Refresh)
	if err != nil {
		return err
	}

	// Step 2: Upsert repos into SurrealDB
	fmt.Println("Upserting repos into SurrealDB...")
	for i, repo := range repos {
		if err := db.UpsertRepo(ctx, repo); err != nil {
			return err
		}
		if (i+1)%50 == 0 || i+1 == len(repos) {
			fmt.Printf("  Upserted %d/%d\n", i+1, len(repos))
		}
	}

	if opts.SkipEnrich {
		fmt.Println("Skipping enrichment (--skip-enrich)")
		return nil
	}

	// Step 3: Find repos needing enrichment
	var toEnrich []models.Repo
	if opts.Force {
		toEnrich, err = db.GetAllRepos(ctx)
	} else {
		toEnrich, err = db.GetUnenrichedRepos(ctx)
	}
	if err != nil {
		return err
	}

	if len(toEnrich) == 0 {
		fmt.Println("All repos already enriched")
	} else {
		// Step 4: Generate AI summaries
		fmt.Printf("Enriching %d repos with AI summaries...\n", len(toEnrich))
		llmClient := llm.NewClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)

		var done atomic.Int64
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(5)

		for _, repo := range toEnrich {
			repo := repo
			g.Go(func() error {
				result, err := llmClient.Summarize(gCtx, repo)
				if err != nil {
					fmt.Printf("  WARN: %v\n", err)
					return nil // continue with other repos
				}

				if err := db.UpdateEnrichment(gCtx, repo.FullName, result.Summary, result.Categories); err != nil {
					fmt.Printf("  WARN: storing enrichment for %s: %v\n", repo.FullName, err)
					return nil
				}

				n := done.Add(1)
				if n%10 == 0 || int(n) == len(toEnrich) {
					fmt.Printf("  Enriched %d/%d\n", n, len(toEnrich))
				}
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return err
		}
		fmt.Printf("Enrichment complete (%d repos)\n", done.Load())
	}

	// Step 5: Generate embeddings
	var toEmbed []models.Repo
	if opts.Force {
		toEmbed, err = db.GetAllRepos(ctx)
	} else {
		toEmbed, err = db.GetReposNeedingEmbedding(ctx)
	}
	if err != nil {
		return err
	}

	if len(toEmbed) == 0 {
		fmt.Println("All repos already have embeddings")
	} else {
		fmt.Printf("Generating embeddings for %d repos...\n", len(toEmbed))
		embClient := embedding.NewClient(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)

		// Build input texts
		texts := make([]string, len(toEmbed))
		for i, repo := range toEmbed {
			summary := ""
			if repo.AISummary != nil {
				summary = *repo.AISummary
			}
			texts[i] = fmt.Sprintf("%s: %s", repo.FullName, summary)
		}

		vectors, err := embClient.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("generating embeddings: %w", err)
		}

		// Store embeddings
		fmt.Println("Storing embeddings...")
		for i, repo := range toEmbed {
			if err := db.UpdateEmbedding(ctx, repo.FullName, vectors[i]); err != nil {
				fmt.Printf("  WARN: storing embedding for %s: %v\n", repo.FullName, err)
				continue
			}
		}
		fmt.Printf("Stored %d embeddings\n", len(vectors))
	}

	fmt.Println("Sync complete!")
	return nil
}

func loadRepos(ctx context.Context, cfg *config.Config, refresh bool) ([]models.Repo, error) {
	gh := github.NewClient(cfg.GitHubToken)
	cached, cacheErr := readCache()

	// --refresh: discard cache and do a full forward fetch
	if refresh {
		fmt.Println("Fetching star list from GitHub (full refresh)...")
		return fetchAndCache(ctx, gh, cfg.StarListID, github.ForwardStrategy{}, nil)
	}

	// Cache exists: try incremental fetch for new repos
	if cacheErr == nil && len(cached) > 0 {
		fmt.Printf("Cache has %d repos. Checking for new stars...\n", len(cached))
		repos, err := github.IncrementalStrategy{}.Fetch(ctx, gh, cfg.StarListID, cached)
		if err != nil {
			fmt.Printf("  WARN: incremental fetch failed (%v), using cache as-is\n", err)
			return cached, nil
		}
		if len(repos) > len(cached) {
			fmt.Printf("Found %d new repos (%d total)\n", len(repos)-len(cached), len(repos))
			if err := writeCache(repos); err != nil {
				fmt.Printf("  WARN: could not update %s: %v\n", cacheFile, err)
			}
		} else {
			fmt.Printf("Cache is up to date (%d repos)\n", len(cached))
		}
		return repos, nil
	}

	// No cache: full forward fetch
	fmt.Println("Fetching star list from GitHub...")
	return fetchAndCache(ctx, gh, cfg.StarListID, github.ForwardStrategy{}, nil)
}

func fetchAndCache(ctx context.Context, gh *github.Client, listID string, strategy github.Strategy, cached []models.Repo) ([]models.Repo, error) {
	repos, err := strategy.Fetch(ctx, gh, listID, cached)
	if err != nil {
		return nil, fmt.Errorf("fetching star list: %w", err)
	}
	fmt.Printf("Fetched %d repos\n", len(repos))

	if err := writeCache(repos); err != nil {
		fmt.Printf("  WARN: could not cache to %s: %v\n", cacheFile, err)
	} else {
		fmt.Printf("Cached to %s\n", cacheFile)
	}
	return repos, nil
}

func readCache() ([]models.Repo, error) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}
	var repos []models.Repo
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, err
	}
	return repos, nil
}

func writeCache(repos []models.Repo) error {
	data, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, data, 0o644)
}
