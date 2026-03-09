// Package webscrape provides a progressive four-tier web scraping strategy.
// Tiers escalate from cheapest to most expensive:
//   - Tier 1: Plain HTTP GET
//   - Tier 2: Browser-header GET (gzip/deflate decompression)
//   - Tier 3: Headless Chrome via chromedp (JavaScript rendering)
//   - Tier 4: Bright Data residential proxies + CAPTCHA solving
package webscrape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxBodySize = 2 * 1024 * 1024 // 2 MB
	minBodySize = 100             // responses below this are considered empty/blocked
)

// Result holds the scraped content and metadata from a successful fetch.
type Result struct {
	Content     string
	ContentType string        // "html", "text", "markdown"
	Tier        int           // which tier succeeded (1–4)
	URL         string        // final URL after redirects
	StatusCode  int
	Duration    time.Duration
}

// fetchConfig holds per-request overrides applied via FetchOption.
type fetchConfig struct {
	maxTier int
	timeout time.Duration
	headers map[string]string
}

// FetchOption configures a single Fetch call.
type FetchOption func(*fetchConfig)

// WithMaxTier limits this fetch to tiers 1–n (overrides the Fetcher default).
func WithMaxTier(n int) FetchOption {
	return func(c *fetchConfig) { c.maxTier = n }
}

// WithTimeout sets a per-request timeout (wraps the context).
func WithTimeout(d time.Duration) FetchOption {
	return func(c *fetchConfig) { c.timeout = d }
}

// WithHeaders adds extra headers to HTTP-based tiers (1 and 2).
func WithHeaders(h map[string]string) FetchOption {
	return func(c *fetchConfig) { c.headers = h }
}

// Fetcher performs progressive web scraping using a four-tier escalation strategy.
// Create with New and configure with SetBrightDataKey / SetMaxTier.
// Call Close when done to release any browser resources.
type Fetcher struct {
	client        *http.Client
	logger        *slog.Logger
	maxTier       int    // default 4 — all tiers enabled
	brightDataKey string // Tier 4 API key; empty disables Tier 4
	brightDataURL string // override for testing; defaults to brightDataEndpoint
	skipSSRF      bool   // for tests only — bypass SSRF validation

	// Tier 3: headless Chrome — lazily initialized on first use, shared across requests
	chromeOnce   sync.Once
	chromeAlloc  context.Context
	chromeCancel context.CancelFunc
}

// New creates a Fetcher with all tiers enabled and no Bright Data key.
func New(client *http.Client, logger *slog.Logger) *Fetcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fetcher{
		client:  client,
		logger:  logger,
		maxTier: 4,
	}
}

// SetBrightDataKey configures the Bright Data API key for Tier 4.
// Returns the Fetcher for chaining.
func (f *Fetcher) SetBrightDataKey(key string) *Fetcher {
	f.brightDataKey = key
	return f
}

// SetMaxTier limits scraping to tiers 1–n. Valid values: 1–4.
// Returns the Fetcher for chaining.
func (f *Fetcher) SetMaxTier(n int) *Fetcher {
	f.maxTier = n
	return f
}

// SkipSSRF disables SSRF URL validation. Use only in tests.
func (f *Fetcher) SkipSSRF() *Fetcher {
	f.skipSSRF = true
	return f
}

// SetBrightDataURL overrides the Bright Data API endpoint. Use in tests.
func (f *Fetcher) SetBrightDataURL(url string) *Fetcher {
	f.brightDataURL = url
	return f
}

// Close releases resources held by the Fetcher, including any headless Chrome
// process started by Tier 3. Safe to call multiple times or if Tier 3 was never used.
func (f *Fetcher) Close() {
	if f.chromeCancel != nil {
		f.chromeCancel()
	}
}

// Fetch retrieves content from rawURL using progressive tier escalation.
// It validates the URL for SSRF before attempting any fetch.
// "Success" means: HTTP 200, body > 100 bytes.
// Each tier is only attempted if maxTier allows it.
// Per-request overrides can be applied via FetchOption.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, opts ...FetchOption) (*Result, error) {
	cfg := fetchConfig{maxTier: f.maxTier}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}

	validURL := rawURL
	if !f.skipSSRF {
		var err error
		validURL, err = ValidateURL(rawURL)
		if err != nil {
			return nil, fmt.Errorf("ssrf: %w", err)
		}
	}

	var lastErr error

	if cfg.maxTier >= 1 {
		start := time.Now()
		result, err := f.fetchTier1(ctx, validURL, cfg.headers)
		if err == nil {
			result.Duration = time.Since(start)
			f.logger.Debug("fetch tier 1 success", "url", validURL)
			return result, nil
		}
		lastErr = err
		f.logger.Debug("fetch tier 1 failed", "url", validURL, "error", err)
	}

	if cfg.maxTier >= 2 {
		start := time.Now()
		result, err := f.fetchTier2(ctx, validURL, cfg.headers)
		if err == nil {
			result.Duration = time.Since(start)
			f.logger.Debug("fetch tier 2 success", "url", validURL)
			return result, nil
		}
		lastErr = err
		f.logger.Debug("fetch tier 2 failed", "url", validURL, "error", err)
	}

	if cfg.maxTier >= 3 {
		start := time.Now()
		result, err := f.fetchTier3(ctx, validURL)
		if err == nil {
			result.Duration = time.Since(start)
			f.logger.Debug("fetch tier 3 success", "url", validURL)
			return result, nil
		}
		lastErr = err
		f.logger.Debug("fetch tier 3 failed", "url", validURL, "error", err)
	}

	if cfg.maxTier >= 4 {
		// Skip Tier 4 if disabled (no key) without logging it as a failure
		if f.brightDataKey == "" {
			if lastErr != nil {
				return nil, fmt.Errorf("all tiers failed for %s: %w", validURL, lastErr)
			}
			return nil, fmt.Errorf("no tiers available (maxTier=%d, bright data disabled)", cfg.maxTier)
		}
		start := time.Now()
		result, err := f.fetchTier4(ctx, validURL)
		if err != nil && !errors.Is(err, errTier4Disabled) {
			lastErr = err
			f.logger.Debug("fetch tier 4 failed", "url", validURL, "error", err)
		} else if err == nil {
			result.Duration = time.Since(start)
			f.logger.Debug("fetch tier 4 success", "url", validURL)
			return result, nil
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all tiers failed for %s: %w", validURL, lastErr)
	}
	return nil, fmt.Errorf("no tiers configured (maxTier=%d)", cfg.maxTier)
}

// fetchTier1 performs a plain GET with minimal headers.
// extraHeaders are applied on top of the defaults (may be nil).
func (f *Fetcher) fetchTier1(ctx context.Context, rawURL string, extraHeaders map[string]string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,text/plain")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return readResponse(resp, 1)
}

// readResponse reads and validates a response body against success criteria.
func readResponse(resp *http.Response, tier int) (*Result, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if len(body) < minBodySize {
		return nil, fmt.Errorf("body too small (%d bytes)", len(body))
	}

	ct := detectContentType(resp.Header.Get("Content-Type"))
	return &Result{
		Content:     string(body),
		ContentType: ct,
		Tier:        tier,
		URL:         resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
	}, nil
}

// detectContentType normalizes a Content-Type header value to "html", "text", or "markdown".
func detectContentType(ct string) string {
	switch {
	case strings.Contains(ct, "text/html"):
		return "html"
	case strings.Contains(ct, "text/markdown"):
		return "markdown"
	default:
		return "text"
	}
}
