package study

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/justinpbarnett/virgil/internal/webscrape"
)

// htmlBody is a minimal valid HTML page exceeding the 100-byte minimum.
const webTestHTML = `<!DOCTYPE html><html><body><article><h1>Go Concurrency</h1><p>Goroutines and channels enable concurrent programming in Go. The scheduler multiplexes goroutines onto OS threads efficiently.</p></article></body></html>`

func newTestHTTPServer(body string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		if body != "" {
			fmt.Fprint(w, body)
		}
	}))
}

func TestWebBackendURLEntry(t *testing.T) {
	srv := newTestHTTPServer(webTestHTML, http.StatusOK)
	defer srv.Close()

	fetcher := webscrape.New(srv.Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	b := newWebBackend(fetcher, nil, slog.Default())

	// Discover: entry URL → 1 candidate
	candidates, err := b.Discover(context.Background(), "", srv.URL, "normal")
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Location.Path != srv.URL {
		t.Errorf("expected path %q, got %q", srv.URL, candidates[0].Location.Path)
	}

	// Extract: fetches and extracts text
	items, err := b.Extract(context.Background(), candidates, "general")
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.Contains(items[0].Content, "Goroutines") {
		t.Errorf("expected article content, got: %q", items[0].Content)
	}
	if items[0].TokenCount <= 0 {
		t.Errorf("expected positive token count, got %d", items[0].TokenCount)
	}
}

func TestWebBackendQueryWithSearcher(t *testing.T) {
	// Content server
	contentSrv := newTestHTTPServer(webTestHTML, http.StatusOK)
	defer contentSrv.Close()

	// Mock searcher that returns the content server URL
	mockSearcher := &mockSearcher{
		results: []webscrape.SearchResult{
			{Title: "Go Concurrency", URL: contentSrv.URL, Snippet: "Goroutines"},
			{Title: "More Go", URL: contentSrv.URL + "/more", Snippet: "Channels"},
		},
	}

	fetcher := webscrape.New(contentSrv.Client(), slog.Default()).SetMaxTier(2)
	b := newWebBackend(fetcher, mockSearcher, slog.Default())

	candidates, err := b.Discover(context.Background(), "golang concurrency", "", "normal")
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestWebBackendQueryWithoutSearcher(t *testing.T) {
	b := newWebBackend(nil, nil, slog.Default())

	_, err := b.Discover(context.Background(), "golang concurrency", "", "normal")
	if err == nil {
		t.Error("expected error when searcher is nil")
	}
	if !strings.Contains(err.Error(), "entry flag") && !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected helpful error message, got: %v", err)
	}
}

func TestWebBackendEmptyQueryAndEntry(t *testing.T) {
	b := newWebBackend(nil, nil, slog.Default())
	_, err := b.Discover(context.Background(), "", "", "normal")
	if err == nil {
		t.Error("expected error for empty query and entry")
	}
}

func TestWebBackendFetchFailureSkipped(t *testing.T) {
	// One server succeeds, one fails
	goodSrv := newTestHTTPServer(webTestHTML, http.StatusOK)
	defer goodSrv.Close()

	badSrv := newTestHTTPServer("", http.StatusInternalServerError)
	defer badSrv.Close()

	fetcher := webscrape.New(goodSrv.Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	b := newWebBackend(fetcher, nil, slog.Default())

	candidates := []Candidate{
		{ID: goodSrv.URL, Source: "web", Location: Location{Path: goodSrv.URL}},
		{ID: badSrv.URL, Source: "web", Location: Location{Path: badSrv.URL}},
	}

	items, err := b.Extract(context.Background(), candidates, "general")
	// Should succeed — good server provides content
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item (bad URL skipped), got %d", len(items))
	}
}

func TestWebBackendAllFetchesFail(t *testing.T) {
	badSrv := newTestHTTPServer("", http.StatusServiceUnavailable)
	defer badSrv.Close()

	fetcher := webscrape.New(badSrv.Client(), slog.Default()).SetMaxTier(2)
	b := newWebBackend(fetcher, nil, slog.Default())

	candidates := []Candidate{
		{ID: badSrv.URL, Source: "web", Location: Location{Path: badSrv.URL}},
	}
	_, err := b.Extract(context.Background(), candidates, "general")
	if err == nil {
		t.Error("expected error when all fetches fail")
	}
}

func TestWebBackendHTMLExtraction(t *testing.T) {
	srv := newTestHTTPServer(webTestHTML, http.StatusOK)
	defer srv.Close()

	fetcher := webscrape.New(srv.Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	b := newWebBackend(fetcher, nil, slog.Default())

	candidates := []Candidate{
		{ID: srv.URL, Source: "web", Location: Location{Path: srv.URL}},
	}
	items, err := b.Extract(context.Background(), candidates, "general")
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one item")
	}
	// Should contain extracted article text, not raw HTML tags
	content := items[0].Content
	if strings.Contains(content, "<html>") || strings.Contains(content, "<body>") {
		t.Errorf("raw HTML tags found in extracted content: %q", content[:min(200, len(content))])
	}
	if !strings.Contains(content, "Goroutines") {
		t.Errorf("expected article content in extraction, got: %q", content)
	}
}

func TestWebBackendTokenCounting(t *testing.T) {
	srv := newTestHTTPServer(webTestHTML, http.StatusOK)
	defer srv.Close()

	fetcher := webscrape.New(srv.Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	b := newWebBackend(fetcher, nil, slog.Default())

	candidates := []Candidate{
		{ID: srv.URL, Source: "web", Location: Location{Path: srv.URL}},
	}
	items, err := b.Extract(context.Background(), candidates, "general")
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected item")
	}
	if items[0].TokenCount <= 0 {
		t.Errorf("expected positive token count, got %d", items[0].TokenCount)
	}
	// Token count should roughly match CountTokens of the content
	expected := CountTokens(items[0].Content)
	if items[0].TokenCount != expected {
		t.Errorf("token count mismatch: stored %d, computed %d", items[0].TokenCount, expected)
	}
}

func TestWebBackendConcurrencyLimit(t *testing.T) {
	// Track peak concurrency across 5 servers; max 3 should be in flight at once
	var mu sync.Mutex
	var current, peak int

	var servers []*httptest.Server
	var candidates []Candidate
	for i := 0; i < 5; i++ {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			current++
			if current > peak {
				peak = current
			}
			mu.Unlock()

			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, webTestHTML)

			mu.Lock()
			current--
			mu.Unlock()
		}))
		servers = append(servers, srv)
		candidates = append(candidates, Candidate{
			ID:     srv.URL,
			Source: "web",
			Location: Location{Path: srv.URL},
		})
	}
	defer func() {
		for _, srv := range servers {
			srv.Close()
		}
	}()

	fetcher := webscrape.New(servers[0].Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	b := newWebBackend(fetcher, nil, slog.Default())

	items, err := b.Extract(context.Background(), candidates, "general")
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if len(items) == 0 {
		t.Error("expected items from multiple URLs")
	}
	if peak > 3 {
		t.Errorf("peak concurrency %d exceeded limit of 3", peak)
	}
}

// mockSearcher implements webscrape.Searcher for tests.
type mockSearcher struct {
	results []webscrape.SearchResult
	err     error
}

func (m *mockSearcher) Search(_ context.Context, _ string, limit int) ([]webscrape.SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	if limit > 0 && len(m.results) > limit {
		return m.results[:limit], nil
	}
	return m.results, nil
}
