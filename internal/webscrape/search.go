package webscrape

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
)

// Searcher discovers URLs relevant to a query.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// SearchResult is a single result from a web search.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// SearXNGSearcher queries a self-hosted SearXNG instance.
// It is zero-cost and requires no API keys.
type SearXNGSearcher struct {
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

// NewSearXNGSearcher creates a searcher pointing at a SearXNG instance.
// baseURL should be the root URL, e.g. "http://localhost:8888".
func NewSearXNGSearcher(baseURL string, client *http.Client, logger *slog.Logger) *SearXNGSearcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &SearXNGSearcher{
		baseURL: baseURL,
		client:  client,
		logger:  logger,
	}
}

// searxngResponse is the JSON shape returned by SearXNG's /search endpoint.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// Search queries SearXNG for the given query and returns up to limit results.
func (s *SearXNGSearcher) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("empty search query")
	}
	if limit <= 0 {
		limit = 10
	}

	endpoint := s.baseURL + "/search"
	params := url.Values{
		"q":       {query},
		"format":  {"json"},
		"engines": {"google,duckduckgo"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searxng returned HTTP %d", resp.StatusCode)
	}

	var sr searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}

	results := make([]SearchResult, 0, min(limit, len(sr.Results)))
	for i, r := range sr.Results {
		if i >= limit {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}

	s.logger.Debug("searxng search complete", "query", query, "results", len(results))
	return results, nil
}
