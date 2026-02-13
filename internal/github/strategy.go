package github

import (
	"context"
	"fmt"

	"github.com/kevinmichaelchen/star-watch/internal/models"
)

// Strategy determines how repos are fetched from a GitHub star list.
//
// The UserList.items GraphQL connection is undocumented and its sort order
// is not guaranteed. Strategies encapsulate the pagination approach so we
// can swap implementations if GitHub changes behavior.
type Strategy interface {
	// Fetch returns repos from the star list. cached contains previously
	// fetched repos (may be nil on first run). The returned slice should
	// be the complete set of repos to cache.
	Fetch(ctx context.Context, c *Client, listID string, cached []models.Repo) ([]models.Repo, error)
}

// ForwardStrategy fetches all repos via forward pagination (first/after).
// This is the most reliable approach — it makes no assumptions about sort
// order and always returns the complete list.
type ForwardStrategy struct{}

func (ForwardStrategy) Fetch(ctx context.Context, c *Client, listID string, _ []models.Repo) ([]models.Repo, error) {
	var allRepos []models.Repo
	var cursor *string

	for {
		page, err := c.FetchPageForward(ctx, listID, cursor)
		if err != nil {
			return nil, err
		}

		allRepos = append(allRepos, page.Repos...)
		fmt.Printf("  Fetched %d/%d repos\n", len(allRepos), page.TotalCount)

		if !page.PageInfo.HasNextPage {
			break
		}
		cursor = &page.PageInfo.EndCursor
	}

	return allRepos, nil
}

// IncrementalStrategy fetches only new repos by paginating backward from the
// end of the list. It assumes the list is ordered oldest-starred first (the
// observed default for UserList.items), so new additions appear at the end.
//
// The strategy fetches the last page, checks for repos not in the cache, and
// continues backward until it hits a known repo. New repos are appended to
// the cached set.
//
// Falls back to ForwardStrategy if the cache is empty.
type IncrementalStrategy struct{}

func (IncrementalStrategy) Fetch(ctx context.Context, c *Client, listID string, cached []models.Repo) ([]models.Repo, error) {
	if len(cached) == 0 {
		fmt.Println("  No cache — falling back to full fetch")
		return ForwardStrategy{}.Fetch(ctx, c, listID, cached)
	}

	known := make(map[string]bool, len(cached))
	for _, r := range cached {
		known[r.FullName] = true
	}

	// Paginate backward (newest first). Collect pages of new repos,
	// then reverse page order so the final slice is oldest-to-newest.
	var pages [][]models.Repo
	var cursor *string

	for {
		page, err := c.FetchPageBackward(ctx, listID, cursor)
		if err != nil {
			return nil, err
		}

		// Within a backward page, items are still in connection order
		// (oldest to newest). Split into new and known.
		var newOnPage []models.Repo
		hitKnown := false
		for _, repo := range page.Repos {
			if known[repo.FullName] {
				hitKnown = true
			} else {
				newOnPage = append(newOnPage, repo)
			}
		}

		if len(newOnPage) > 0 {
			pages = append(pages, newOnPage)
		}

		if hitKnown || !page.PageInfo.HasPreviousPage {
			break
		}
		cursor = &page.PageInfo.StartCursor
	}

	if len(pages) == 0 {
		return cached, nil
	}

	// Reverse page order: we fetched last→first, but want oldest→newest.
	var newRepos []models.Repo
	for i := len(pages) - 1; i >= 0; i-- {
		newRepos = append(newRepos, pages[i]...)
	}

	fmt.Printf("  Found %d new repos\n", len(newRepos))
	return append(cached, newRepos...), nil
}
