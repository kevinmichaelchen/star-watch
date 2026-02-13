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

	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string

	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string
}

func Load() *Config {
	_ = godotenv.Load()

	cfg := &Config{
		SurrealURL:  os.Getenv("SURREAL_URL"),
		SurrealNS:   os.Getenv("SURREAL_NS"),
		SurrealDB:   os.Getenv("SURREAL_DB"),
		SurrealUser: os.Getenv("SURREAL_USER"),
		SurrealPass: os.Getenv("SURREAL_PASS"),

		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		StarListID:  os.Getenv("STAR_LIST_ID"),

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
