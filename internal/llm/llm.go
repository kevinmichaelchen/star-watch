package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kevinmichaelchen/star-watch/internal/models"
	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
	model  string
}

func NewClient(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

const systemPrompt = `You are a technical analyst. Given a GitHub repository's name, description, and README excerpt, produce a JSON object with:

1. "summary": A 2-3 sentence summary of what the repo does, its main use case, and why it's notable.
2. "categories": An array of 1-3 categories from this list:
   LLM Framework, Vector Database, ML Training, NLP, Computer Vision, AI Agent, RAG, Model Serving, Data Pipeline, Developer Tool, Library/SDK, Research, Observability, Other

Return ONLY valid JSON. No markdown, no code fences.`

func (c *Client) Summarize(ctx context.Context, repo models.Repo) (*models.SummaryResult, error) {
	var parts []string
	parts = append(parts, fmt.Sprintf("Repository: %s", repo.FullName))
	if repo.Description != nil {
		parts = append(parts, fmt.Sprintf("Description: %s", *repo.Description))
	}
	if repo.ReadmeExcerpt != nil {
		parts = append(parts, fmt.Sprintf("README excerpt:\n%s", *repo.ReadmeExcerpt))
	}
	userMsg := strings.Join(parts, "\n\n")

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userMsg},
		},
		// No ResponseFormat — not all models support json_object mode.
		// The system prompt instructs the model to return pure JSON.
		Temperature: 0.3,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call for %s: %w", repo.FullName, err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned for %s", repo.FullName)
	}

	content := resp.Choices[0].Message.Content
	content = stripCodeFences(content)

	var result models.SummaryResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parsing LLM response for %s: %w\nraw: %s", repo.FullName, err, content)
	}

	return &result, nil
}

// stripCodeFences removes markdown code fences that some models wrap around JSON.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json or ```)
		if i := strings.Index(s, "\n"); i != -1 {
			s = s[i+1:]
		}
		// Remove closing fence
		if i := strings.LastIndex(s, "```"); i != -1 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
