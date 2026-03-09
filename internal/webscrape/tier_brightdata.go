package webscrape

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	brightDataEndpoint = "https://api.brightdata.com/request"
	tier4Timeout       = 15 * time.Second
)

// errTier4Disabled is returned when Bright Data is not configured.
// The Fetch loop treats this as a signal to stop escalation (not a real error).
var errTier4Disabled = errors.New("tier 4 disabled: no Bright Data API key configured")

type brightDataRequest struct {
	URL    string `json:"url"`
	Format string `json:"format"`
}

// fetchTier4 uses Bright Data's Web Scraper API for residential proxies and
// CAPTCHA solving. It is the last resort when all free tiers fail.
// Returns errTier4Disabled if no API key is configured.
func (f *Fetcher) fetchTier4(ctx context.Context, rawURL string) (*Result, error) {
	if f.brightDataKey == "" {
		return nil, errTier4Disabled
	}

	payload, err := json.Marshal(brightDataRequest{
		URL:    rawURL,
		Format: "raw",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, tier4Timeout)
	defer cancel()

	endpoint := brightDataEndpoint
	if f.brightDataURL != "" {
		endpoint = f.brightDataURL
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.brightDataKey)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bright data request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("bright data HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Log usage headers for cost observability
	if cost := resp.Header.Get("X-Cost"); cost != "" {
		f.logger.Info("bright data cost", "url", rawURL, "cost", cost)
	}
	if credits := resp.Header.Get("X-Credits-Remaining"); credits != "" {
		f.logger.Debug("bright data credits remaining", "credits", credits)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if len(body) < minBodySize {
		return nil, fmt.Errorf("bright data returned too little content (%d bytes)", len(body))
	}

	ct := detectContentType(resp.Header.Get("Content-Type"))
	return &Result{
		Content:     string(body),
		ContentType: ct,
		Tier:        4,
		URL:         rawURL,
		StatusCode:  resp.StatusCode,
	}, nil
}
