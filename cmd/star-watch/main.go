package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

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

const defaultFields = "full_name,description,ai_summary,ai_categories,stars,created_at,url,score"

func searchCmd() *cobra.Command {
	var (
		k           int
		pool        int
		jsonOut     bool
		markdownOut bool
		fieldsRaw   string
		sortRaw     string
	)

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Semantic similarity search across repos",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg := config.Load()
			query := args[0]
			if jsonOut && markdownOut {
				return fmt.Errorf("--json and --markdown are mutually exclusive")
			}
			if k <= 0 {
				return fmt.Errorf("--k must be > 0")
			}
			if pool < 0 {
				return fmt.Errorf("--pool must be >= 0")
			}

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

			searchK := k
			searchSort := sortSpecs
			searchFields := mergeFields(fields, sortSpecs)

			// Optional two-stage retrieval:
			// 1) fetch top semantic candidates by score
			// 2) apply user sort (e.g. stars desc) in-memory
			if pool > 0 {
				searchK = pool
				if searchK < k {
					searchK = k
				}
				searchSort = []surrealdb.SortSpec{{Field: "score", Desc: true}}
			}

			results, err := db.VectorSearch(ctx, vec, surrealdb.SearchOptions{
				K:      searchK,
				Fields: searchFields,
				Sort:   searchSort,
			})
			if err != nil {
				return err
			}
			if pool > 0 {
				sortResultMaps(results, sortSpecs)
				if len(results) > k {
					results = results[:k]
				}
			}

			if len(results) == 0 {
				if jsonOut {
					fmt.Println("[]")
				} else if markdownOut {
					printMarkdownTable(nil, fields)
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
			if markdownOut {
				printMarkdownTable(filtered, fields)
				return nil
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
	cmd.Flags().IntVar(&pool, "pool", 0, "Semantic candidate pool size before applying --sort (0 disables re-ranking)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON array")
	cmd.Flags().BoolVar(&markdownOut, "markdown", false, "Output as Markdown table")
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

func mergeFields(fields []string, sortSpecs []surrealdb.SortSpec) []string {
	wanted := make(map[string]bool, len(fields)+len(sortSpecs)+1)
	out := make([]string, 0, len(fields)+len(sortSpecs)+1)
	for _, f := range fields {
		if wanted[f] {
			continue
		}
		wanted[f] = true
		out = append(out, f)
	}
	for _, s := range sortSpecs {
		if wanted[s.Field] {
			continue
		}
		wanted[s.Field] = true
		out = append(out, s.Field)
	}
	if !wanted["score"] {
		out = append(out, "score")
	}
	return out
}

func sortResultMaps(results []map[string]any, sortSpecs []surrealdb.SortSpec) {
	if len(results) < 2 || len(sortSpecs) == 0 {
		return
	}
	sort.SliceStable(results, func(i, j int) bool {
		left := results[i]
		right := results[j]
		for _, s := range sortSpecs {
			cmp := compareValues(left[s.Field], right[s.Field], s.Field)
			if cmp == 0 {
				continue
			}
			if s.Desc {
				return cmp > 0
			}
			return cmp < 0
		}
		li, _ := left["full_name"].(string)
		ri, _ := right["full_name"].(string)
		return li < ri
	})
}

func compareValues(a, b any, field string) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	if ta, ok := asTime(a); ok {
		if tb, ok := asTime(b); ok {
			switch {
			case ta.Before(tb):
				return -1
			case ta.After(tb):
				return 1
			default:
				return 0
			}
		}
	}

	if fa, ok := asFloat(a); ok {
		if fb, ok := asFloat(b); ok {
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			default:
				return 0
			}
		}
	}

	sa := strings.ToLower(formatCellValue(field, a))
	sb := strings.ToLower(formatCellValue(field, b))
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	default:
		return 0
	}
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

func printMarkdownTable(results []map[string]any, fields []string) {
	headers := make([]string, len(fields))
	for i, f := range fields {
		headers[i] = displayFieldName(f)
	}

	fmt.Printf("| %s |\n", strings.Join(headers, " | "))

	separators := make([]string, len(fields))
	for i := range separators {
		separators[i] = "---"
	}
	fmt.Printf("| %s |\n", strings.Join(separators, " | "))

	for _, row := range results {
		cells := make([]string, len(fields))
		for i, f := range fields {
			cells[i] = escapeMarkdown(formatCellValue(f, row[f]))
		}
		fmt.Printf("| %s |\n", strings.Join(cells, " | "))
	}
}

func displayFieldName(field string) string {
	switch field {
	case "full_name":
		return "Repo"
	case "stars":
		return "Stars"
	case "created_at":
		return "Created"
	case "url":
		return "URL"
	case "ai_categories":
		return "Categories"
	case "ai_summary":
		return "Summary"
	case "score":
		return "Score"
	default:
		return strings.ReplaceAll(field, "_", " ")
	}
}

func formatCellValue(field string, v any) string {
	if v == nil {
		return ""
	}
	if field == "url" {
		if s, ok := v.(string); ok && s != "" {
			return fmt.Sprintf("[%s](%s)", s, s)
		}
	}
	if t, ok := asTime(v); ok {
		if strings.HasSuffix(field, "_at") {
			return t.UTC().Format("2006-01-02")
		}
		return t.UTC().Format(time.RFC3339)
	}
	if n, ok := asFloat(v); ok {
		if field == "score" {
			return fmt.Sprintf("%.3f", n)
		}
		return strconv.Itoa(int(n))
	}
	if s := toStringSlice(v); len(s) > 0 {
		return strings.Join(s, ", ")
	}
	switch vv := v.(type) {
	case string:
		return vv
	default:
		return fmt.Sprintf("%v", v)
	}
}

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", "<br>")
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

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}

func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			return parsed, true
		}
		if parsed, err := time.Parse("2006-01-02", t); err == nil {
			return parsed, true
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
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
