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

type Client struct {
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{token: token, httpClient: http.DefaultClient}
}

const starListQuery = `
query($listId: ID!, $after: String) {
  node(id: $listId) {
    ... on UserList {
      items(first: 100, after: $after) {
        totalCount
        pageInfo {
          hasNextPage
          endCursor
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
			TotalCount int `json:"totalCount"`
			PageInfo   struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []repoNode `json:"nodes"`
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

func (c *Client) FetchStarList(ctx context.Context, listID string) ([]models.Repo, error) {
	var allRepos []models.Repo
	var cursor *string

	for {
		vars := map[string]any{"listId": listID}
		if cursor != nil {
			vars["after"] = *cursor
		}

		body, err := c.doGraphQL(ctx, starListQuery, vars)
		if err != nil {
			return nil, err
		}

		var data starListData
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("parsing response: %w", err)
		}

		for _, node := range data.Node.Items.Nodes {
			repo := nodeToRepo(node)
			allRepos = append(allRepos, repo)
		}

		fmt.Printf("  Fetched %d/%d repos\n", len(allRepos), data.Node.Items.TotalCount)

		if !data.Node.Items.PageInfo.HasNextPage {
			break
		}
		cursor = &data.Node.Items.PageInfo.EndCursor
	}

	return allRepos, nil
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
