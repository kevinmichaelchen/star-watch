package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	SurrealURL  string
	SurrealNS   string
	SurrealDB   string
	SurrealUser string
	SurrealPass string

	GitHubToken string
	StarListID  string
	StarListIDs []string

	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string
}

func Load() *Config {
	_ = godotenv.Load()
	starListID := strings.TrimSpace(os.Getenv("STAR_LIST_ID"))
	starListIDs := parseListIDs(os.Getenv("STAR_LIST_IDS"))
	if len(starListIDs) == 0 && starListID != "" {
		starListIDs = []string{starListID}
	}
	if starListID == "" && len(starListIDs) > 0 {
		starListID = starListIDs[0]
	}

	cfg := &Config{
		SurrealURL:  os.Getenv("SURREAL_URL"),
		SurrealNS:   os.Getenv("SURREAL_NS"),
		SurrealDB:   os.Getenv("SURREAL_DB"),
		SurrealUser: os.Getenv("SURREAL_USER"),
		SurrealPass: os.Getenv("SURREAL_PASS"),

		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		StarListID:  starListID,
		StarListIDs: starListIDs,

		LLMBaseURL: os.Getenv("LLM_BASE_URL"),
		LLMAPIKey:  os.Getenv("LLM_API_KEY"),
		LLMModel:   os.Getenv("LLM_MODEL"),

		EmbeddingBaseURL: os.Getenv("EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:  os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:   os.Getenv("EMBEDDING_MODEL"),
	}

	// The SDK appends /rpc automatically
	cfg.SurrealURL = strings.TrimSuffix(cfg.SurrealURL, "/rpc")
	cfg.SurrealURL = strings.TrimSuffix(cfg.SurrealURL, "/")

	// LLM defaults: Fireworks GLM-5
	if cfg.LLMBaseURL == "" {
		cfg.LLMBaseURL = "https://api.fireworks.ai/inference/v1"
	}
	if cfg.LLMAPIKey == "" {
		cfg.LLMAPIKey = os.Getenv("FIREWORKS_API_KEY")
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = "accounts/fireworks/models/glm-5"
	}

	// Embedding defaults: Fireworks nomic-embed-text
	if cfg.EmbeddingBaseURL == "" {
		cfg.EmbeddingBaseURL = "https://api.fireworks.ai/inference/v1"
	}
	if cfg.EmbeddingAPIKey == "" {
		cfg.EmbeddingAPIKey = os.Getenv("FIREWORKS_API_KEY")
	}
	if cfg.EmbeddingModel == "" {
		cfg.EmbeddingModel = "nomic-ai/nomic-embed-text-v1.5"
	}

	return cfg
}

func parseListIDs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(parts) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
