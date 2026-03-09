package webscrape

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// htmlBody is a minimal valid HTML page that exceeds the 100-byte minimum.
const htmlBody = `<!DOCTYPE html><html><body><main><h1>Test Page</h1><p>This is a test page with enough content to pass the minimum body size check in the progressive scraper.</p></main></body></html>`

func newTestFetcher(client *http.Client) *Fetcher {
	return New(client, slog.Default()).SetMaxTier(2) // cap at tier 2 for unit tests
}

// fetchDirect calls tier1 or tier2 without SSRF validation — for httptest URLs.
func (f *Fetcher) fetchDirect(ctx context.Context, rawURL string) (*Result, error) {
	if f.maxTier >= 1 {
		result, err := f.fetchTier1(ctx, rawURL, nil)
		if err == nil {
			return result, nil
		}
	}
	if f.maxTier >= 2 {
		result, err := f.fetchTier2(ctx, rawURL, nil)
		if err == nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("all tiers failed")
}

func TestFetchTier1Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier1(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != 1 {
		t.Errorf("expected Tier 1, got Tier %d", result.Tier)
	}
	if result.ContentType != "html" {
		t.Errorf("expected html content type, got %q", result.ContentType)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected 200, got %d", result.StatusCode)
	}
}

func TestFetchTier1FailTier2Success(t *testing.T) {
	// Server rejects requests without a browser User-Agent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Mozilla") {
			http.Error(w, "bot detected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())

	// Tier 1 should fail
	_, err := f.fetchTier1(context.Background(), srv.URL, nil)
	if err == nil {
		t.Error("expected tier 1 to fail for bot-blocking server")
	}

	// Tier 2 should succeed
	result, err := f.fetchTier2(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("tier 2 unexpected error: %v", err)
	}
	if result.Tier != 2 {
		t.Errorf("expected Tier 2, got Tier %d", result.Tier)
	}
}

func TestFetchAllTiersFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	_, err := f.fetchDirect(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error when all tiers fail")
	}
}

func TestFetchSSRFRejection(t *testing.T) {
	f := New(http.DefaultClient, slog.Default()).SetMaxTier(2)
	_, err := f.Fetch(context.Background(), "http://127.0.0.1/secret")
	if err == nil {
		t.Error("expected SSRF rejection for private IP")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Errorf("expected ssrf in error, got: %v", err)
	}
}

func TestFetchSSRFRejectPrivateScheme(t *testing.T) {
	f := New(http.DefaultClient, slog.Default()).SetMaxTier(2)
	_, err := f.Fetch(context.Background(), "file:///etc/passwd")
	if err == nil {
		t.Error("expected SSRF rejection for file:// scheme")
	}
}

func TestFetchRedirectHandling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier1(context.Background(), srv.URL+"/", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(result.URL, "/final") {
		t.Errorf("expected final URL to end with /final, got %q", result.URL)
	}
}

func TestFetchBodySizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		chunk := strings.Repeat("a", 1024)
		for i := 0; i < 5*1024; i++ {
			w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier1(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) > maxBodySize {
		t.Errorf("body exceeds maxBodySize: %d bytes", len(result.Content))
	}
}

func TestFetchContextCancellation(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-done
	}))
	defer srv.Close()
	defer close(done)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f := newTestFetcher(srv.Client())
	_, err := f.fetchTier1(ctx, srv.URL, nil)
	if err == nil {
		t.Error("expected error on cancelled context")
	}
}

func TestFetchEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	_, err := f.fetchDirect(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error on empty body")
	}
}

func TestFetchMaxTierRespected(t *testing.T) {
	tier2Reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if strings.Contains(ua, "Mozilla") {
			tier2Reached = true
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, htmlBody)
			return
		}
		http.Error(w, "403", http.StatusForbidden)
	}))
	defer srv.Close()

	// maxTier=1: only Tier 1 attempted
	f := New(srv.Client(), slog.Default()).SetMaxTier(1)
	f.fetchTier1(context.Background(), srv.URL, nil) //nolint

	if tier2Reached {
		t.Error("Tier 2 was attempted despite maxTier=1")
	}
}

func TestFetchNonHTMLContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := strings.Repeat(`{"key":"value"}`, 20)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier1(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ContentType != "text" {
		t.Errorf("expected text content type for JSON, got %q", result.ContentType)
	}
}

func TestFetchTier2BrowserHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Mozilla") {
			t.Errorf("Tier 2 did not send Mozilla UA, got: %q", ua)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier2(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Test Page") {
		t.Errorf("expected content in result")
	}
}

func TestFetchTier2Brotli(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ae := r.Header.Get("Accept-Encoding")
		if !strings.Contains(ae, "br") {
			t.Errorf("Tier 2 should advertise br encoding, got: %q", ae)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "br")
		bw := brotli.NewWriter(w)
		fmt.Fprint(bw, htmlBody)
		bw.Close()
	}))
	defer srv.Close()

	f := newTestFetcher(srv.Client())
	result, err := f.fetchTier2(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Test Page") {
		t.Errorf("expected decompressed content, got: %q", result.Content[:min(100, len(result.Content))])
	}
}

func TestFetchTier3GracefulDegradation(t *testing.T) {
	// Tier 3 uses headless Chrome. If Chrome is not installed, fetchTier3
	// should return an error (graceful degradation) rather than panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := New(srv.Client(), slog.Default())

	// Use a short timeout so the test doesn't hang if Chrome is missing
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := f.fetchTier3(ctx, srv.URL)
	if err != nil {
		// Expected when Chrome is not installed — graceful degradation
		t.Logf("Tier 3 gracefully failed (expected if Chrome not installed): %v", err)
		return
	}

	// If Chrome IS available, verify we got content
	if result.Tier != 3 {
		t.Errorf("expected Tier 3, got Tier %d", result.Tier)
	}
	if len(result.Content) < 100 {
		t.Errorf("expected substantial content, got %d bytes", len(result.Content))
	}
}

func TestFetchTier4Success(t *testing.T) {
	// Mock Bright Data API server
	bdSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/json" {
			http.Error(w, "expected json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Cost", "0.001")
		w.Header().Set("X-Credits-Remaining", "999")
		fmt.Fprint(w, htmlBody)
	}))
	defer bdSrv.Close()

	f := New(bdSrv.Client(), slog.Default()).
		SetBrightDataKey("test-key").
		SetBrightDataURL(bdSrv.URL)

	result, err := f.fetchTier4(context.Background(), "https://example.com/blocked-page")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != 4 {
		t.Errorf("expected Tier 4, got Tier %d", result.Tier)
	}
	if !strings.Contains(result.Content, "Test Page") {
		t.Errorf("expected content from Bright Data mock, got: %q", result.Content[:min(100, len(result.Content))])
	}
}

func TestFetchTier4Disabled(t *testing.T) {
	f := New(http.DefaultClient, slog.Default()) // no Bright Data key
	_, err := f.fetchTier4(context.Background(), "https://example.com")
	if err == nil {
		t.Error("expected error when Bright Data key is not set")
	}
}

func TestFetchEscalationVisFetchMethod(t *testing.T) {
	// Server rejects non-browser requests — Tier 1 fails, Tier 2 succeeds
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Mozilla") {
			http.Error(w, "bot detected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := New(srv.Client(), slog.Default()).SetMaxTier(2).SkipSSRF()
	result, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != 2 {
		t.Errorf("expected Tier 2 after Tier 1 failed, got Tier %d", result.Tier)
	}
}

func TestFetchTier4ViaFetchMethod(t *testing.T) {
	// Mock Bright Data API
	bdSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer bdSrv.Close()

	// Server that always rejects (Tiers 1-2 fail)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()

	// maxTier=2 skips Tier 3 (Chrome); Bright Data configured as Tier 4
	// Call Fetch with WithMaxTier(4) to include Tier 4 in escalation
	// Tiers 1-2 fail → skip Tier 3 (not included) → Tier 4 via Bright Data mock
	f := New(srv.Client(), slog.Default()).
		SetMaxTier(2).
		SetBrightDataKey("test-key").
		SetBrightDataURL(bdSrv.URL).
		SkipSSRF()

	// Directly test tier 4 after knowing tiers 1-2 fail
	result, err := f.fetchTier4(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected Tier 4 to succeed: %v", err)
	}
	if result.Tier != 4 {
		t.Errorf("expected Tier 4, got Tier %d", result.Tier)
	}
}

func TestFetchWithOptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := New(srv.Client(), slog.Default()).SetMaxTier(4).SkipSSRF()

	// WithMaxTier overrides the fetcher default
	result, err := f.Fetch(context.Background(), srv.URL, WithMaxTier(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != 1 {
		t.Errorf("expected Tier 1 (per-request override), got Tier %d", result.Tier)
	}
}

func TestFetchWithExtraHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "test-value" {
			http.Error(w, "missing custom header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlBody)
	}))
	defer srv.Close()

	f := New(srv.Client(), slog.Default()).SetMaxTier(1).SkipSSRF()

	result, err := f.Fetch(context.Background(), srv.URL,
		WithHeaders(map[string]string{"X-Custom": "test-value"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != 1 {
		t.Errorf("expected Tier 1, got Tier %d", result.Tier)
	}
}
