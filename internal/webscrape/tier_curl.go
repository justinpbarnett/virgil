package webscrape

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

// browserHeaders simulates a modern browser's request headers.
// These help bypass basic bot-detection that checks for a real browser User-Agent.
var browserHeaders = map[string]string{
	"User-Agent":                "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
	"Accept-Language":           "en-US,en;q=0.9",
	"Accept-Encoding":           "gzip, deflate, br",
	"Sec-Fetch-Dest":            "document",
	"Sec-Fetch-Mode":            "navigate",
	"Sec-Fetch-Site":            "none",
	"Sec-Fetch-User":            "?1",
	"Upgrade-Insecure-Requests": "1",
	"Cache-Control":             "no-cache",
	"Pragma":                    "no-cache",
}

// fetchTier2 performs an HTTP GET with full browser header simulation.
// It handles gzip, deflate, and brotli compression manually since Accept-Encoding
// is set explicitly (bypassing http.Transport's automatic decompression).
// extraHeaders are applied on top of the browser defaults (may be nil).
func (f *Fetcher) fetchTier2(ctx context.Context, rawURL string, extraHeaders map[string]string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range browserHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Decompress if needed — required because we set Accept-Encoding explicitly
	body, err := decompressResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	if len(body) < minBodySize {
		return nil, fmt.Errorf("body too small (%d bytes)", len(body))
	}

	ct := detectContentType(resp.Header.Get("Content-Type"))
	return &Result{
		Content:     string(body),
		ContentType: ct,
		Tier:        2,
		URL:         resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
	}, nil
}

// decompressResponse reads and decompresses the response body based on Content-Encoding.
func decompressResponse(resp *http.Response) ([]byte, error) {
	encoding := strings.ToLower(resp.Header.Get("Content-Encoding"))

	var reader io.Reader
	switch encoding {
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz

	case "deflate":
		reader = flate.NewReader(resp.Body)

	case "br":
		reader = brotli.NewReader(resp.Body)

	default:
		reader = resp.Body
	}

	return io.ReadAll(io.LimitReader(reader, maxBodySize))
}
