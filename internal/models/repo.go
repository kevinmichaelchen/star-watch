package models

type Repo struct {
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   *string   `json:"description"`
	URL           string    `json:"url"`
	HomepageURL   *string   `json:"homepage_url"`
	Stars         int       `json:"stars"`
	Language      *string   `json:"language"`
	Topics        []string  `json:"topics"`
	ReadmeExcerpt *string   `json:"readme_excerpt"`
	AISummary     *string   `json:"ai_summary"`
	AICategories  []string  `json:"ai_categories"`
	Embedding     []float32 `json:"embedding"`
}

type SummaryResult struct {
	Summary    string   `json:"summary"`
	Categories []string `json:"categories"`
}
