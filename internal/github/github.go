package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/kevinmichaelchen/star-watch/internal/models"
)

const graphqlEndpoint = "https://api.github.com/graphql"

// Client is a thin wrapper around the GitHub GraphQL API.
type Client struct {
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, httpClient: http.DefaultClient}
}

// pageQuery supports both forward (first/after) and backward (last/before)
// Relay pagination via nullable variables.
const pageQuery = `
query($listId: ID!, $first: Int, $after: String, $last: Int, $before: String) {
  node(id: $listId) {
    ... on UserList {
      items(first: $first, after: $after, last: $last, before: $before) {
        totalCount
        pageInfo {
          hasNextPage
          endCursor
          hasPreviousPage
          startCursor
        }
        nodes {
          ... on Repository {
            owner { login }
            name
            description
            url
            homepageUrl
            stargazerCount
            primaryLanguage { name }
            repositoryTopics(first: 20) {
              nodes { topic { name } }
            }
            object(expression: "HEAD:README.md") {
              ... on Blob { text }
            }
          }
        }
      }
    }
  }
}
`

// Page holds one page of results from the star list connection.
type Page struct {
	TotalCount int
	PageInfo   PageInfo
	Repos      []models.Repo
}

type PageInfo struct {
	HasNextPage     bool   `json:"hasNextPage"`
	EndCursor       string `json:"endCursor"`
	HasPreviousPage bool   `json:"hasPreviousPage"`
	StartCursor     string `json:"startCursor"`
}

// FetchPageForward returns one page of results using forward pagination
// (oldest first). Pass nil for after to start from the beginning.
func (c *Client) FetchPageForward(ctx context.Context, listID string, after *string) (*Page, error) {
	vars := map[string]any{
		"listId": listID,
		"first":  100,
	}
	if after != nil {
		vars["after"] = *after
	}
	return c.fetchPage(ctx, vars)
}

// FetchPageBackward returns one page of results using backward pagination
// (newest first). Pass nil for before to start from the end.
func (c *Client) FetchPageBackward(ctx context.Context, listID string, before *string) (*Page, error) {
	vars := map[string]any{
		"listId": listID,
		"last":   100,
	}
	if before != nil {
		vars["before"] = *before
	}
	return c.fetchPage(ctx, vars)
}

// --- internal ---

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type starListData struct {
	Node struct {
		Items struct {
			TotalCount int      `json:"totalCount"`
			PageInfo   PageInfo `json:"pageInfo"`
			Nodes      []repoNode
		} `json:"items"`
	} `json:"node"`
}

type repoNode struct {
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	URL             string  `json:"url"`
	HomepageURL     *string `json:"homepageUrl"`
	StargazerCount  int     `json:"stargazerCount"`
	PrimaryLanguage *struct {
		Name string `json:"name"`
	} `json:"primaryLanguage"`
	RepositoryTopics struct {
		Nodes []struct {
			Topic struct {
				Name string `json:"name"`
			} `json:"topic"`
		} `json:"nodes"`
	} `json:"repositoryTopics"`
	Object *struct {
		Text string `json:"text"`
	} `json:"object"`
}

func (c *Client) fetchPage(ctx context.Context, vars map[string]any) (*Page, error) {
	body, err := c.doGraphQL(ctx, pageQuery, vars)
	if err != nil {
		return nil, err
	}

	var data starListData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	repos := make([]models.Repo, 0, len(data.Node.Items.Nodes))
	for _, node := range data.Node.Items.Nodes {
		repos = append(repos, nodeToRepo(node))
	}

	return &Page{
		TotalCount: data.Node.Items.TotalCount,
		PageInfo:   data.Node.Items.PageInfo,
		Repos:      repos,
	}, nil
}

func (c *Client) doGraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	reqBody, err := json.Marshal(graphqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("parsing GraphQL response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

func nodeToRepo(n repoNode) models.Repo {
	r := models.Repo{
		Owner:       n.Owner.Login,
		Name:        n.Name,
		FullName:    n.Owner.Login + "/" + n.Name,
		Description: n.Description,
		URL:         n.URL,
		HomepageURL: n.HomepageURL,
		Stars:       n.StargazerCount,
	}

	if n.PrimaryLanguage != nil {
		r.Language = &n.PrimaryLanguage.Name
	}

	var topics []string
	for _, t := range n.RepositoryTopics.Nodes {
		topics = append(topics, t.Topic.Name)
	}
	if topics == nil {
		topics = []string{}
	}
	r.Topics = topics

	if n.Object != nil && n.Object.Text != "" {
		text := n.Object.Text
		const maxLen = 3000
		if len(text) > maxLen {
			text = text[:maxLen]
		}
		r.ReadmeExcerpt = &text
	}

	return r
}
