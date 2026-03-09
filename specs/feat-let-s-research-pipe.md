# Feature: Progressive Web Source for Study Pipe

## Metadata

type: `feat`
task_id: `research-pipe`
prompt: Implement the study pipe's web source backend using a progressive four-tier scraping strategy — start simple, escalate intelligently, only use advanced resources when necessary.

## Feature Description

The study pipe already declares `source: web` but returns "web source not yet implemented." This feature implements the web source backend using Daniel Miessler's progressive web scraping pattern: a four-tier fallback system that starts with the cheapest/fastest method and escalates only when simpler tiers fail.

The progressive scraper is reusable infrastructure. It lives in its own package (`internal/webscrape/`) so any pipe — study, chat, spec, or future pipes — can fetch web content without duplicating scraping logic.

The study pipe's existing discover → extract → rank → compress pipeline stays unchanged. The web backend simply implements the `SourceBackend` interface, using the progressive scraper internally to fetch and extract content from URLs.

### Why Not a Separate Research Pipe?

The study pipe already owns the research domain:
- `pipe.yaml` declares `source: web` as a valid flag value
- The "research" keyword routes to study (`vocabulary.verbs.research: [study]`)
- The discover → extract → rank → compress pipeline is exactly the flow research needs
- A separate pipe would duplicate infrastructure and conflict with routing

## User Story

As a **Virgil user**, I want to say "study web for {topic}" or "research {topic}" so that Virgil retrieves, scrapes, and synthesizes web content into compressed context — the same way it does for codebase or memory sources.

As a **pipeline author**, I want a reusable `webscrape.Fetch(url)` function so that any pipe can reliably extract content from a URL without knowing the scraping details.

## Relevant Files

### Existing Files — Modify

| File | Why |
|---|---|
| `internal/pipes/study/study.go` | Wire the new `webBackend` into `selectBackend()` — replace the `"web source not yet implemented"` error |
| `internal/pipes/study/discover.go` | Contains the `SourceBackend` interface that the web backend must implement |
| `internal/pipes/study/cmd/main.go` | Pass HTTP client and optional API keys to the study handler config |
| `internal/pipes/study/pipe.yaml` | Add web-specific vocabulary (sources: web, url, site, page; verbs: scrape, fetch) |

### New Files — Create

| File | Purpose |
|---|---|
| `internal/webscrape/webscrape.go` | Progressive scraper: `Fetch(ctx, url) → (content, tier, error)` — tries tiers 1→4 sequentially |
| `internal/webscrape/webscrape_test.go` | Unit tests with httptest servers simulating each tier's failure modes |
| `internal/webscrape/tier_curl.go` | Tier 2: HTTP GET with full browser header simulation |
| `internal/webscrape/tier_browser.go` | Tier 3: Headless browser via `chromedp` — full JS rendering |
| `internal/webscrape/tier_brightdata.go` | Tier 4: Bright Data MCP — residential proxies, CAPTCHA solving |
| `internal/webscrape/ssrf.go` | URL validation — block private/internal IP ranges (RFC 1918, localhost, link-local) |
| `internal/webscrape/ssrf_test.go` | SSRF validation tests |
| `internal/webscrape/html.go` | HTML→text extraction: readability-style content extraction from raw HTML |
| `internal/webscrape/html_test.go` | HTML extraction tests |
| `internal/webscrape/search.go` | `Searcher` interface + `SearXNGSearcher` implementation |
| `internal/webscrape/search_test.go` | Search integration tests |
| `internal/pipes/study/web.go` | `webBackend` implementing `SourceBackend` — uses `webscrape.Fetch()` + search API for discovery |
| `internal/pipes/study/web_test.go` | Web backend tests |

## Implementation Plan

### Phase 1: Progressive Scraper Package (Tiers 1-2)

Build `internal/webscrape/` with Tier 1 (simple GET) and Tier 2 (browser-header GET), plus SSRF protection and HTML→text extraction. No pipe integration yet. Fully testable in isolation.

### Phase 2: Advanced Tiers (3-4)

Add Tier 3 (headless browser via `chromedp` — JS rendering for SPAs) and Tier 4 (Bright Data MCP — residential proxies, CAPTCHA solving for hardened sites).

### Phase 3: Study Pipe Web Backend

Implement `webBackend` conforming to `SourceBackend`, wire it into the study pipe's `selectBackend()` switch.

### Phase 4: Search-Based Discovery

Add SearXNG integration so `source: web` can discover URLs from a query (not just scrape a given URL). Zero-cost, self-hosted.

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Define the progressive scraper types and Fetch function

File: `internal/webscrape/webscrape.go`

- Define `Result` struct: `Content string`, `ContentType string` (html, text, markdown), `Tier int`, `URL string`, `StatusCode int`, `Duration time.Duration`
- Define `FetchOption` functional options: `WithTimeout(d)`, `WithMaxTier(n)`, `WithHeaders(map[string]string)`
- Define `Fetcher` struct holding an `*http.Client` and config
- Implement `func (f *Fetcher) Fetch(ctx context.Context, url string, opts ...FetchOption) (*Result, error)`:
  - Validate URL (must be http/https, pass SSRF check)
  - Try Tier 1 (simple GET), if success → return
  - Try Tier 2 (curl-style headers), if success → return
  - Try Tier 3 (chromedp headless browser), if success → return
  - Try Tier 4 (Bright Data MCP), if success → return
  - Return `Result` with the tier that succeeded
  - Each tier only attempted if `maxTier` config allows it
- "Success" means: HTTP 200, response body > 100 bytes, content-type is text/html or text/plain
- Non-200 status codes, empty bodies, or connection errors trigger escalation to next tier

```go
type Result struct {
    Content     string
    ContentType string        // "html", "text", "markdown"
    Tier        int           // which tier succeeded (1-4)
    URL         string        // final URL after redirects
    StatusCode  int
    Duration    time.Duration
}

type Fetcher struct {
    client       *http.Client
    logger       *slog.Logger
    maxTier      int    // default 4 — all tiers enabled
    brightDataKey string // Tier 4 API key, empty disables Tier 4
}

func New(client *http.Client, logger *slog.Logger) *Fetcher
func (f *Fetcher) Fetch(ctx context.Context, url string) (*Result, error)
```

### 2. Implement Tier 1: Simple GET

File: `internal/webscrape/webscrape.go` (inside `Fetch`)

- Plain `http.Get` with the fetcher's client
- Minimal headers: just `Accept: text/html`
- 10-second timeout (from context)
- Read body up to 2MB limit
- Check success criteria: status 200, body length > 100 bytes

### 3. Implement Tier 2: Browser-Header GET

File: `internal/webscrape/tier_curl.go`

- Same HTTP client, but with full browser header set:
  ```
  User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 ...
  Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8
  Accept-Language: en-US,en;q=0.9
  Accept-Encoding: gzip, deflate, br
  Sec-Fetch-Dest: document
  Sec-Fetch-Mode: navigate
  Sec-Fetch-Site: none
  Sec-Fetch-User: ?1
  Upgrade-Insecure-Requests: 1
  ```
- Handle gzip/br decompression
- Follow redirects (up to 5)
- Same 2MB body limit and success criteria

### 4. Implement SSRF protection

File: `internal/webscrape/ssrf.go`

- `func ValidateURL(rawURL string) (string, error)` — parse, normalize, and validate
- Block schemes other than `http` and `https`
- Resolve hostname to IP, block if IP falls in:
  - RFC 1918: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
  - Loopback: `127.0.0.0/8`, `::1`
  - Link-local: `169.254.0.0/16`, `fe80::/10`
  - Multicast, broadcast, unspecified (`0.0.0.0`)
- Return normalized URL on success
- Called at the top of `Fetcher.Fetch()` before any tier attempts

### 5. Write SSRF protection tests

File: `internal/webscrape/ssrf_test.go`

- Valid public URLs pass
- `http://127.0.0.1`, `http://localhost`, `http://[::1]` → rejected
- `http://10.0.0.1`, `http://192.168.1.1`, `http://172.16.0.1` → rejected
- `http://169.254.169.254` (cloud metadata) → rejected
- `ftp://`, `file://`, `javascript:` schemes → rejected
- DNS rebinding: hostname that resolves to private IP → rejected (resolved at validation time)
- Valid HTTPS URLs pass

### 6. Implement Tier 3: Headless Browser

File: `internal/webscrape/tier_browser.go`

- Uses `chromedp` for full headless Chrome execution
- `func (f *Fetcher) fetchTier3(ctx context.Context, url string) (*Result, error)`
- Create a new browser context with 20-second timeout
- Navigate to URL, wait for `document.readyState === "complete"` or `networkIdle`
- Extract rendered DOM via `document.documentElement.outerHTML`
- Return HTML content with `Tier: 3`
- Handles: JavaScript-rendered SPAs (React, Vue, Angular), cookie consent walls, lazy-loaded content
- Graceful degradation: if Chrome/Chromium not installed, log warning and skip to Tier 4

### 7. Implement Tier 4: Bright Data MCP

File: `internal/webscrape/tier_brightdata.go`

- `func (f *Fetcher) fetchTier4(ctx context.Context, url string) (*Result, error)`
- Calls Bright Data's Web Scraper API (REST):
  - `POST https://api.brightdata.com/request` with API key header
  - Body: `{"url": url, "format": "raw"}`
  - Response: scraped HTML content
- If `brightDataKey` is empty, skip tier (return sentinel error to continue escalation)
- 15-second timeout
- Return HTML content with `Tier: 4`
- Log cost/usage info from response headers for observability

### 8. Implement HTML→text extraction

File: `internal/webscrape/html.go`

- `func ExtractText(htmlContent string) (string, error)` — readability-style extraction
- Use `golang.org/x/net/html` to parse the DOM
- Remove `<script>`, `<style>`, `<nav>`, `<header>`, `<footer>`, `<aside>` elements
- Extract text from `<article>`, `<main>`, `<p>`, `<h1>`-`<h6>`, `<li>`, `<td>`, `<blockquote>`, `<pre>`, `<code>`
- Preserve heading hierarchy (prefix with `# `, `## `, etc.)
- Preserve code blocks (wrap in triple backticks)
- Collapse whitespace, trim empty lines
- If `<article>` or `<main>` exists, prefer its content over full-page extraction
- Return clean text suitable for tokenization and ranking

### 9. Write HTML extraction tests

File: `internal/webscrape/html_test.go`

- Article extraction: `<article>` content preferred over sidebar/nav
- Script/style removal: no JavaScript or CSS in output
- Heading preservation: `<h2>Title</h2>` → `## Title`
- Code block preservation: `<pre><code>...</code></pre>` → fenced code block
- Whitespace normalization: no runs of blank lines
- Empty/minimal HTML: returns empty string, no error
- Malformed HTML: graceful degradation (best-effort extraction)

### 10. Write progressive scraper tests

File: `internal/webscrape/webscrape_test.go`

- Tier 1 success: simple server returns HTML → Tier 1 result
- Tier 1 fail, Tier 2 success: server checks User-Agent, rejects basic requests → Tier 2 result
- Tier 1+2 fail, Tier 3 success: JS-rendered content only available via headless browser
- All free tiers fail, Tier 4 success: Bright Data returns content
- All tiers fail: returns error with last tier's status
- SSRF rejection: private IP URLs rejected before any tier runs
- Redirect handling: 301 → final URL recorded in result
- Body size limit: 5MB response → only first 2MB read
- Context cancellation: returns context error immediately
- Empty body: triggers escalation
- Non-HTML content type: still returns content (for text/plain, application/json)
- `maxTier` respected: `maxTier=2` skips Tiers 3-4 even on failure

### 11. Implement the web SourceBackend for study pipe

File: `internal/pipes/study/web.go`

- `webBackend` struct: holds `*webscrape.Fetcher`, optional `webscrape.Searcher`, `*slog.Logger`
- Max 3 concurrent URL fetches via `errgroup` with semaphore
- Implement `SourceBackend` interface:
  - `Discover(ctx, query, entry, depth)`:
    - If `entry` is a URL → single candidate from that URL
    - If `entry` is empty and `query` is provided → use Searcher to find URLs
    - If Searcher is nil and no URL entry → return error "web search not configured; provide a URL via entry flag"
    - Return `[]Candidate` with Source="web", Location.Path=URL
  - `Extract(ctx, candidates, role)`:
    - For each candidate URL, call `webscrape.Fetch(ctx, url)`
    - Run `webscrape.ExtractText()` on HTML results
    - Return `[]ExtractedItem` with content, token count, metadata
    - Parallel extraction with `errgroup`, max 3 concurrent, per-URL timeout
    - Individual failures logged but non-fatal (skip that URL)

```go
type webBackend struct {
    fetcher  *webscrape.Fetcher
    searcher webscrape.Searcher // nil = URL-only mode
    logger   *slog.Logger
}

func newWebBackend(fetcher *webscrape.Fetcher, searcher webscrape.Searcher, logger *slog.Logger) *webBackend

func (b *webBackend) Discover(ctx context.Context, query, entry, depth string) ([]Candidate, error)
func (b *webBackend) Extract(ctx context.Context, candidates []Candidate, role string) ([]ExtractedItem, error)
```

### 12. Write web backend tests

File: `internal/pipes/study/web_test.go`

- URL entry: `entry="https://example.com"` → discovers 1 candidate → extracts content
- Query with searcher: `query="golang concurrency"` → searcher returns URLs → discovers candidates
- Query without searcher: returns clear error message
- Empty query and entry: returns error
- Fetch failure: candidate skipped, no error if other candidates succeed
- All fetches fail: returns error
- HTML extraction: raw HTML → clean text in ExtractedItem.Content
- Token counting: content has accurate token estimate
- Concurrency limit: 10 URLs → max 3 fetched in parallel

### 13. Wire webBackend into selectBackend

File: `internal/pipes/study/study.go`

- Replace the `case "web": return nil, fmt.Errorf("web source not yet implemented")` with:
  ```go
  case "web":
      return newWebBackend(cfg.Fetcher, cfg.Searcher, p.logger), nil
  ```
- Add `Fetcher *webscrape.Fetcher` and `Searcher webscrape.Searcher` to the `Config` struct

### 14. Update study pipe cmd/main.go

File: `internal/pipes/study/cmd/main.go`

- Create `http.Client` with 30s timeout
- Create `webscrape.Fetcher` with the client, maxTier=4
- Read `VIRGIL_BRIGHTDATA_KEY` env var → pass to fetcher config
- Optionally create `SearXNGSearcher` if `VIRGIL_SEARXNG_URL` is set (default: not configured)
- Pass fetcher and searcher to `study.Config`

### 15. Update study pipe.yaml vocabulary

File: `internal/pipes/study/pipe.yaml`

- Add to vocabulary.sources: `web: [web]`, `url: [web]`, `site: [web]`, `page: [web]`, `article: [web]`
- Add to triggers.patterns: `"research {topic} online"`, `"fetch {entry}"`
- Add to triggers.keywords: `scrape`, `fetch`, `webpage`

### 16. Implement search-based URL discovery

File: `internal/webscrape/search.go`

- `Searcher` interface: `Search(ctx, query, limit) ([]SearchResult, error)`
- `SearchResult`: `Title string`, `URL string`, `Snippet string`
- Implement `SearXNGSearcher` — queries a self-hosted SearXNG instance (JSON API)
  - Configurable base URL (default `http://localhost:8888`)
  - `/search?q={query}&format=json&engines=google,duckduckgo`
  - Zero-cost, no API keys
- Update `webBackend.Discover()`: when no URL entry is provided, use Searcher to find URLs from the query
- Searcher is optional — if nil, URL-only mode (must provide entry)

## Testing Strategy

### Unit Tests

| Test File | Covers |
|---|---|
| `internal/webscrape/webscrape_test.go` | Progressive 4-tier escalation, maxTier config, timeout, redirect, body limits |
| `internal/webscrape/ssrf_test.go` | Private IP blocking, scheme validation, DNS rebinding |
| `internal/webscrape/html_test.go` | HTML→text extraction, script removal, heading preservation |
| `internal/webscrape/search_test.go` | SearXNG integration, query formatting, result parsing |
| `internal/pipes/study/web_test.go` | SourceBackend contract, URL discovery, parallel extraction, concurrency limits |

### Integration Tests

- End-to-end: `source=web, entry=https://example.com` → study pipe returns structured output with web content
- Pipeline composition: study(source=web) → chat — web context feeds into conversation

### Edge Cases

- URL with JavaScript-only content → Tiers 1/2 return near-empty body → Tier 3 (chromedp) renders JS and extracts
- URL returns 429 (rate limit) → escalation to next tier
- URL returns massive page (>2MB) → truncated, still extracted
- HTTPS certificate error → logged, candidate skipped
- Redirect loop → max 5 redirects, then error
- Non-English content → extracted as-is, no language filtering
- PDF/binary URL → content-type check, skip extraction (return raw URL as reference)

## Risk Assessment

| Risk | Impact | Mitigation |
|---|---|---|
| Sites blocking Tier 1/2 → no content | Query returns empty results | Tier 3 (chromedp) handles JS-rendered sites; Tier 4 (Bright Data) handles anti-bot |
| Bright Data costs accumulate | Unexpected bills | Tier 4 is last resort — only reached when 3 free tiers fail. Log usage for monitoring. |
| chromedp requires Chrome/Chromium installed | Tier 3 fails on minimal systems | Graceful skip — log warning, continue to Tier 4. Document Chrome as optional dependency. |
| Slow pages increase study pipe latency | TUI feels unresponsive for web source | Per-URL timeout (10s per tier), total backend timeout (30s), max 3 concurrent fetches |
| HTML extraction misses main content | Poor quality context in study output | Prefer `<article>`/`<main>`, fallback to full body; improve heuristics iteratively |
| Large pages consume token budget | Other study items crowded out | 2MB body cap, study pipe's existing rank/compress handles budget allocation |
| SearXNG unavailable | Query-based web discovery fails | Graceful degradation: error message suggests using `entry` flag with direct URL |
| SSRF via user-provided URLs | Internal network exposure | Block RFC 1918, loopback, link-local, and cloud metadata IPs before any fetch |

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```bash
# Unit tests for new packages
go test ./internal/webscrape/... -v -count=1

# Study pipe tests (existing + new web backend)
go test ./internal/pipes/study/... -v -count=1

# Race detector for parallel extraction
go test ./internal/pipes/study/... -race -count=1

# Full test suite
just test

# Build verification
just build

# Lint
just lint
```

## Open Questions (Unresolved)

All questions resolved:

| # | Question | Decision |
|---|---|---|
| 1 | Tier 3 (chromedp headless browser)? | **Yes** — included in Phase 2. Handles JS-rendered SPAs. Graceful skip if Chrome not installed. |
| 2 | Tier 4 (Bright Data MCP)? | **Yes** — included in Phase 2. Last-resort tier. Requires `VIRGIL_BRIGHTDATA_KEY` env var; skipped if not set. |
| 3 | Search engine for URL discovery? | **SearXNG** (self-hosted). Configured via `VIRGIL_SEARXNG_URL`. URL-only mode if not configured. |
| 4 | Cache scraped content? | **Yes** — 24h TTL, keyed by normalized URL. Follow-up task after core scraper ships. |
| 5 | Block private IP ranges? | **Yes** — SSRF protection in `internal/webscrape/ssrf.go`. Blocks RFC 1918, loopback, link-local, cloud metadata. |
| 6 | Max concurrent fetches? | **3** concurrent URL fetches via `errgroup` semaphore. |

## Sub-Tasks

Single task — no decomposition needed. All four phases are sequential and tightly coupled.
