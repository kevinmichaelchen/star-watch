package embedding

import (
	"context"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
	model  openai.EmbeddingModel
}

func NewClient(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		client: openai.NewClientWithConfig(cfg),
		model:  openai.EmbeddingModel(model),
	}
}

const maxBatchSize = 256

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	vectors := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += maxBatchSize {
		end := start + maxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		resp, err := c.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
			Input: batch,
			Model: c.model,
		})
		if err != nil {
			return nil, fmt.Errorf("creating embeddings (batch %d-%d): %w", start, end, err)
		}

		for _, emb := range resp.Data {
			vectors[start+emb.Index] = emb.Embedding
		}
	}
	return vectors, nil
}

func (c *Client) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return vecs[0], nil
}
