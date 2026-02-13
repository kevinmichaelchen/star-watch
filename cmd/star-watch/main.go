package main

import (
	"context"
	"encoding/json"
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

const defaultFields = "full_name,description,ai_summary,ai_categories,stars,url,score"

func searchCmd() *cobra.Command {
	var (
		k         int
		jsonOut   bool
		fieldsRaw string
		sortRaw   string
	)

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Semantic similarity search across repos",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg := config.Load()
			query := args[0]

			fields, err := parseFields(fieldsRaw)
			if err != nil {
				return err
			}
			sortSpecs, err := parseSort(sortRaw)
			if err != nil {
				return err
			}

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

			results, err := db.VectorSearch(ctx, vec, surrealdb.SearchOptions{
				K:      k,
				Fields: fields,
				Sort:   sortSpecs,
			})
			if err != nil {
				return err
			}

			if len(results) == 0 {
				if jsonOut {
					fmt.Println("[]")
				} else {
					fmt.Println("No results found")
				}
				return nil
			}

			// Strip keys not in the requested field set.
			filtered := filterFields(results, fields)

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(filtered)
			}

			// Human-readable output
			fmt.Printf("Top %d results for %q:\n\n", len(filtered), query)
			for i, r := range filtered {
				fullName, _ := r["full_name"].(string)
				score := toFloat(r["score"])
				stars := toInt(r["stars"])
				url, _ := r["url"].(string)

				fmt.Printf("%d. %s  (%.3f)  ★ %d\n", i+1, fullName, score, stars)
				if url != "" {
					fmt.Printf("   %s\n", url)
				}
				if s, ok := r["ai_summary"].(string); ok && s != "" {
					fmt.Printf("   %s\n", s)
				}
				if cats := toStringSlice(r["ai_categories"]); len(cats) > 0 {
					fmt.Printf("   Tags: %s\n", strings.Join(cats, ", "))
				}
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&k, "k", "k", 10, "Number of results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON array")
	cmd.Flags().StringVar(&fieldsRaw, "fields", defaultFields, "Comma-separated field names")
	cmd.Flags().StringVar(&sortRaw, "sort", "score desc", "Comma-separated field [asc|desc] specs")
	return cmd
}

// parseFields validates a comma-separated field list.
func parseFields(raw string) ([]string, error) {
	var fields []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !surrealdb.IsAllowedField(f) {
			return nil, fmt.Errorf("unknown field %q", f)
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no fields specified")
	}
	return fields, nil
}

// parseSort parses "field [asc|desc], ..." into SortSpecs.
func parseSort(raw string) ([]surrealdb.SortSpec, error) {
	var specs []surrealdb.SortSpec
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens := strings.Fields(part)
		field := tokens[0]
		if !surrealdb.IsAllowedField(field) {
			return nil, fmt.Errorf("unknown sort field %q", field)
		}
		desc := false
		if len(tokens) > 1 {
			switch strings.ToLower(tokens[1]) {
			case "desc":
				desc = true
			case "asc":
				// default
			default:
				return nil, fmt.Errorf("invalid sort direction %q (use asc or desc)", tokens[1])
			}
		}
		specs = append(specs, surrealdb.SortSpec{Field: field, Desc: desc})
	}
	return specs, nil
}

// filterFields keeps only the requested keys in each result map.
func filterFields(results []map[string]any, fields []string) []map[string]any {
	wanted := make(map[string]bool, len(fields))
	for _, f := range fields {
		wanted[f] = true
	}
	out := make([]map[string]any, len(results))
	for i, r := range results {
		m := make(map[string]any, len(fields))
		for k, v := range r {
			if wanted[k] {
				m[k] = v
			}
		}
		out[i] = m
	}
	return out
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

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
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
