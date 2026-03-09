package webscrape

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearXNGSearcher(t *testing.T) {
	// Mock SearXNG server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "missing query", http.StatusBadRequest)
			return
		}
		format := r.URL.Query().Get("format")
		if format != "json" {
			http.Error(w, "expected json format", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"results": []map[string]interface{}{
				{"title": "Result 1", "url": "https://example.com/1", "content": "Snippet 1"},
				{"title": "Result 2", "url": "https://example.com/2", "content": "Snippet 2"},
				{"title": "Result 3", "url": "https://example.com/3", "content": "Snippet 3"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	searcher := NewSearXNGSearcher(srv.URL, srv.Client(), slog.Default())

	t.Run("basic search", func(t *testing.T) {
		results, err := searcher.Search(context.Background(), "golang concurrency", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("expected 3 results, got %d", len(results))
		}
		if results[0].Title != "Result 1" {
			t.Errorf("unexpected first result title: %q", results[0].Title)
		}
		if results[0].URL != "https://example.com/1" {
			t.Errorf("unexpected first result URL: %q", results[0].URL)
		}
	})

	t.Run("limit respected", func(t *testing.T) {
		results, err := searcher.Search(context.Background(), "test query", 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("expected 2 results (limit), got %d", len(results))
		}
	})

	t.Run("empty query error", func(t *testing.T) {
		_, err := searcher.Search(context.Background(), "", 5)
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

func TestSearXNGSearcherServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	searcher := NewSearXNGSearcher(srv.URL, srv.Client(), slog.Default())
	_, err := searcher.Search(context.Background(), "test", 5)
	if err == nil {
		t.Error("expected error on server error")
	}
}
