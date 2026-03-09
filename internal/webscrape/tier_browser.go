package webscrape

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

const tier3Timeout = 20 * time.Second

// initChrome lazily creates the shared Chrome allocator. Called via sync.Once.
func (f *Fetcher) initChrome() {
	f.chromeAlloc, f.chromeCancel = chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.UserAgent(browserHeaders["User-Agent"]),
		)...,
	)
}

// fetchTier3 uses a headless Chrome browser to render JavaScript-heavy pages.
// It handles SPAs, cookie consent walls, and lazy-loaded content.
// The Chrome process is started lazily on first call and reused across requests.
// If Chrome/Chromium is not installed, it returns an error and Tier 4 is attempted.
func (f *Fetcher) fetchTier3(ctx context.Context, rawURL string) (*Result, error) {
	f.chromeOnce.Do(f.initChrome)

	// New tab on the shared browser
	tabCtx, tabCancel := chromedp.NewContext(f.chromeAlloc)
	defer tabCancel()

	// Cancel this tab if the request context is cancelled
	stop := context.AfterFunc(ctx, tabCancel)
	defer stop()

	timeoutCtx, timeoutCancel := context.WithTimeout(tabCtx, tier3Timeout)
	defer timeoutCancel()

	var htmlContent string
	var finalURL string
	err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery),
		chromedp.Location(&finalURL),
	)
	if err != nil {
		return nil, fmt.Errorf("headless browser: %w", err)
	}

	if len(htmlContent) < minBodySize {
		return nil, fmt.Errorf("headless browser returned too little content (%d bytes)", len(htmlContent))
	}

	if len(htmlContent) > maxBodySize {
		htmlContent = htmlContent[:maxBodySize]
	}

	if finalURL == "" {
		finalURL = rawURL
	}

	return &Result{
		Content:     htmlContent,
		ContentType: "html",
		Tier:        3,
		URL:         finalURL,
		StatusCode:  200,
	}, nil
}
