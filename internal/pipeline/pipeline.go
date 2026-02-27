package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync/atomic"

	"github.com/kevinmichaelchen/star-watch/internal/config"
	"github.com/kevinmichaelchen/star-watch/internal/embedding"
	"github.com/kevinmichaelchen/star-watch/internal/github"
	"github.com/kevinmichaelchen/star-watch/internal/llm"
	"github.com/kevinmichaelchen/star-watch/internal/models"
	"github.com/kevinmichaelchen/star-watch/internal/surrealdb"
	"golang.org/x/sync/errgroup"
)

const (
	cacheFile    = "stars.json"
	cacheVersion = 2
)

type repoCache struct {
	Version int                      `json:"version"`
	Lists   map[string][]models.Repo `json:"lists"`
}

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
	if len(cfg.StarListIDs) == 0 {
		return nil, fmt.Errorf("STAR_LIST_ID or STAR_LIST_IDS must be set")
	}

	gh := github.NewClient(cfg.GitHubToken)
	cache, cacheErr := readCache(cfg.StarListIDs)
	if cacheErr != nil {
		if !os.IsNotExist(cacheErr) {
			fmt.Printf("  WARN: could not read %s (%v); re-fetching from GitHub\n", cacheFile, cacheErr)
		}
		cache = newRepoCache()
	}

	// Keep only requested list caches so the file reflects the active config.
	requested := make(map[string]bool, len(cfg.StarListIDs))
	for _, listID := range cfg.StarListIDs {
		requested[listID] = true
	}
	cacheChanged := false
	for listID := range cache.Lists {
		if requested[listID] {
			continue
		}
		delete(cache.Lists, listID)
		cacheChanged = true
	}

	merged := make(map[string]models.Repo)

	if refresh {
		fmt.Printf("Fetching %d star list(s) from GitHub (full refresh)...\n", len(cfg.StarListIDs))
		for _, listID := range cfg.StarListIDs {
			fmt.Printf("List %s:\n", listID)
			repos, err := github.ForwardStrategy{}.Fetch(ctx, gh, listID, nil)
			if err != nil {
				return nil, fmt.Errorf("fetching star list %s: %w", listID, err)
			}
			fmt.Printf("  Fetched %d repos\n", len(repos))
			cache.Lists[listID] = repos
			mergeReposByFullName(merged, repos)
		}
		cacheChanged = true
	} else {
		for _, listID := range cfg.StarListIDs {
			cachedRepos, hasCache := cache.Lists[listID]
			var repos []models.Repo
			var err error

			if hasCache {
				fmt.Printf("List %s cache has %d repos. Checking for new stars...\n", listID, len(cachedRepos))
				repos, err = github.IncrementalStrategy{}.Fetch(ctx, gh, listID, cachedRepos)
				if err != nil {
					fmt.Printf("  WARN: incremental fetch for %s failed (%v); using cache as-is\n", listID, err)
					repos = cachedRepos
				}
				if len(repos) > len(cachedRepos) {
					fmt.Printf("  Found %d new repos (%d total)\n", len(repos)-len(cachedRepos), len(repos))
					cacheChanged = true
				} else {
					fmt.Printf("  Cache is up to date (%d repos)\n", len(cachedRepos))
				}
			} else {
				fmt.Printf("No cache for list %s. Fetching from GitHub...\n", listID)
				repos, err = github.ForwardStrategy{}.Fetch(ctx, gh, listID, nil)
				if err != nil {
					return nil, fmt.Errorf("fetching star list %s: %w", listID, err)
				}
				fmt.Printf("  Fetched %d repos\n", len(repos))
				cacheChanged = true
			}

			cache.Lists[listID] = repos
			mergeReposByFullName(merged, repos)
		}
	}

	combined := flattenRepoMap(merged)
	fmt.Printf("Combined %d unique repos across %d list(s)\n", len(combined), len(cfg.StarListIDs))

	if cacheChanged {
		if err := writeCache(cache); err != nil {
			fmt.Printf("  WARN: could not cache to %s: %v\n", cacheFile, err)
		} else {
			fmt.Printf("Cached to %s\n", cacheFile)
		}
	}

	return combined, nil
}

func readCache(listIDs []string) (*repoCache, error) {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	var cache repoCache
	if err := json.Unmarshal(data, &cache); err == nil && cache.Version > 0 {
		if cache.Lists == nil {
			cache.Lists = make(map[string][]models.Repo)
		}
		return &cache, nil
	}

	// Backward compatibility: old cache format was a plain []Repo.
	var legacy []models.Repo
	if err := json.Unmarshal(data, &legacy); err == nil {
		cache := newRepoCache()
		if len(listIDs) > 0 {
			cache.Lists[listIDs[0]] = legacy
		}
		return cache, nil
	}

	return nil, fmt.Errorf("invalid cache format")
}

func writeCache(cache *repoCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cacheFile, data, 0o644)
}

func newRepoCache() *repoCache {
	return &repoCache{
		Version: cacheVersion,
		Lists:   make(map[string][]models.Repo),
	}
}

func mergeReposByFullName(dst map[string]models.Repo, repos []models.Repo) {
	for _, repo := range repos {
		dst[repo.FullName] = repo
	}
}

func flattenRepoMap(reposByName map[string]models.Repo) []models.Repo {
	if len(reposByName) == 0 {
		return nil
	}
	out := make([]models.Repo, 0, len(reposByName))
	for _, repo := range reposByName {
		out = append(out, repo)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].FullName < out[j].FullName
	})
	return out
}
