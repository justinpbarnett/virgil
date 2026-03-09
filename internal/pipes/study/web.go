package study

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/webscrape"
	"golang.org/x/sync/errgroup"
)

// webBackend implements SourceBackend using progressive web scraping.
// URL discovery is done via the Searcher (if configured) or directly from the entry flag.
type webBackend struct {
	fetcher  *webscrape.Fetcher
	searcher webscrape.Searcher // nil = URL-only mode
	logger   *slog.Logger
}

func newWebBackend(fetcher *webscrape.Fetcher, searcher webscrape.Searcher, logger *slog.Logger) *webBackend {
	return &webBackend{fetcher: fetcher, searcher: searcher, logger: logger}
}

// Discover returns URL candidates for the given query or entry point.
// If entry is a URL, it returns a single candidate.
// If entry is empty and a Searcher is configured, it performs a web search.
func (b *webBackend) Discover(ctx context.Context, query, entry, _ string) ([]Candidate, error) {
	if entry != "" {
		// Treat entry as a direct URL
		return []Candidate{{
			ID:     entry,
			Source: "web",
			Location: Location{
				Path: entry,
			},
			Preview:  entry,
			SizeHint: 5000, // rough estimate
		}}, nil
	}

	if query == "" {
		return nil, fmt.Errorf("web source: provide a query or entry URL")
	}

	if b.searcher == nil {
		return nil, fmt.Errorf("web search not configured; provide a URL via the entry flag")
	}

	results, err := b.searcher.Search(ctx, query, 10)
	if err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}

	candidates := make([]Candidate, 0, len(results))
	for _, r := range results {
		candidates = append(candidates, Candidate{
			ID:     r.URL,
			Source: "web",
			Location: Location{
				Path: r.URL,
			},
			Preview:  r.Snippet,
			SizeHint: 5000,
		})
	}

	b.logger.Info("web discover complete", "query", query, "candidates", len(candidates))
	return candidates, nil
}

// Extract fetches and extracts text content from each candidate URL.
// Up to 3 URLs are fetched concurrently. Individual failures are logged but non-fatal.
func (b *webBackend) Extract(ctx context.Context, candidates []Candidate, _ string) ([]ExtractedItem, error) {
	const maxConcurrent = 3
	const perURLTimeout = 30 * time.Second

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrent)

	var mu sync.Mutex
	var items []ExtractedItem

	for _, c := range candidates {
		g.Go(func() error {
			urlCtx, cancel := context.WithTimeout(ctx, perURLTimeout)
			defer cancel()

			item, err := b.fetchAndExtract(urlCtx, c)
			if err != nil {
				b.logger.Debug("web extract skipping URL", "url", c.Location.Path, "error", err)
				return nil // non-fatal — skip this URL
			}

			mu.Lock()
			items = append(items, item)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	if len(items) == 0 && len(candidates) > 0 {
		return nil, fmt.Errorf("all %d web URLs failed to fetch", len(candidates))
	}

	return items, nil
}

// fetchAndExtract fetches a URL and extracts clean text from the HTML response.
func (b *webBackend) fetchAndExtract(ctx context.Context, c Candidate) (ExtractedItem, error) {
	rawURL := c.Location.Path

	result, err := b.fetcher.Fetch(ctx, rawURL)
	if err != nil {
		return ExtractedItem{}, fmt.Errorf("fetch %s: %w", rawURL, err)
	}

	content := result.Content
	if result.ContentType == "html" {
		extracted, err := webscrape.ExtractText(result.Content)
		if err != nil {
			return ExtractedItem{}, fmt.Errorf("extract text from %s: %w", rawURL, err)
		}
		content = extracted
	}

	if content == "" {
		return ExtractedItem{}, fmt.Errorf("no text content extracted from %s", rawURL)
	}

	return ExtractedItem{
		ID:          c.ID,
		Content:     content,
		Granularity: GranBlock,
		TokenCount:  CountTokens(content),
		Metadata: ItemMetadata{
			Path:    rawURL,
			Kind:    KindBlock,
			Modified: time.Now(),
		},
	}, nil
}
