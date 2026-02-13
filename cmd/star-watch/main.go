package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kevinmichaelchen/star-watch/internal/config"
	"github.com/kevinmichaelchen/star-watch/internal/embedding"
	"github.com/kevinmichaelchen/star-watch/internal/pipeline"
	"github.com/kevinmichaelchen/star-watch/internal/surrealdb"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "star-watch",
		Short: "GitHub star list → SurrealDB with AI enrichment",
	}

	root.AddCommand(schemaCmd(), syncCmd(), searchCmd(), statsCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func schemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Initialize/update SurrealDB schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg := config.Load()

			db, err := surrealdb.NewClient(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close(ctx) }()

			if err := db.InitSchema(ctx); err != nil {
				return err
			}
			fmt.Println("Schema initialized")
			return nil
		},
	}
}

func syncCmd() *cobra.Command {
	var skipEnrich, force, refresh bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch star list, enrich with AI, store in SurrealDB",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			return pipeline.Run(context.Background(), cfg, pipeline.Options{
				SkipEnrich: skipEnrich,
				Force:      force,
				Refresh:    refresh,
			})
		},
	}
	cmd.Flags().BoolVar(&skipEnrich, "skip-enrich", false, "Fetch and store only (no AI calls)")
	cmd.Flags().BoolVar(&force, "force", false, "Re-enrich all repos")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Re-fetch from GitHub (ignores cache)")
	return cmd
}

func searchCmd() *cobra.Command {
	var k int

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Semantic similarity search across repos",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg := config.Load()
			query := args[0]

			// Embed the query
			embClient := embedding.NewClient(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
			vec, err := embClient.EmbedSingle(ctx, query)
			if err != nil {
				return fmt.Errorf("embedding query: %w", err)
			}

			// Search SurrealDB
			db, err := surrealdb.NewClient(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close(ctx) }()

			results, err := db.VectorSearch(ctx, vec, k)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Println("No results found")
				return nil
			}

			fmt.Printf("Top %d results for %q:\n\n", len(results), query)
			for i, r := range results {
				fmt.Printf("%d. %s  (%.3f)  ★ %d\n", i+1, r.FullName, r.Score, r.Stars)
				fmt.Printf("   %s\n", r.URL)
				if r.AISummary != nil {
					fmt.Printf("   %s\n", *r.AISummary)
				}
				if len(r.AICategories) > 0 {
					fmt.Printf("   Tags: %s\n", strings.Join(r.AICategories, ", "))
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&k, "k", "k", 10, "Number of results")
	return cmd
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show repo counts and category breakdown",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg := config.Load()

			db, err := surrealdb.NewClient(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close(ctx) }()

			stats, err := db.GetStats(ctx)
			if err != nil {
				return err
			}

			fmt.Printf("Repos:    %d\n", stats.Total)
			fmt.Printf("Enriched: %d\n", stats.Enriched)
			fmt.Printf("Embedded: %d\n", stats.Embedded)

			cats, err := db.GetCategoryBreakdown(ctx)
			if err != nil {
				return err
			}

			if len(cats) > 0 {
				sort.Slice(cats, func(i, j int) bool {
					return cats[i].Count > cats[j].Count
				})
				fmt.Println("\nCategory breakdown:")
				for _, c := range cats {
					fmt.Printf("  %-20s %d\n", c.Category, c.Count)
				}
			}

			return nil
		},
	}
}
