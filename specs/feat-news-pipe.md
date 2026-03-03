# Feature: News Pipe

## Metadata

type: `feat`
task_id: `news-pipe`
prompt: `Deterministic pipe that fetches news from multiple configured sources, collates into a unified list, and acts as a source for composition with draft/summarize.`

## Feature Description

A deterministic pipe that fetches news articles from configured sources (RSS/Atom feeds, JSON APIs), deduplicates and collates results into a unified list, and returns them as `content_type: list`. The pipe declares itself as a vocabulary source so composition templates chain it with `draft` for summarization — "summarize the news" becomes `news.retrieve | draft(type=summary)` automatically.

From the outside, `news` is one atomic capability: "get news." The multi-source fanout, format normalization, deduplication, and error handling are internal implementation details.

## User Story

As a Virgil user
I want to say "what's in the news" or "summarize the news about Go"
So that I get a collated, optionally summarized view of recent news from my configured sources without manually visiting each one.

## Relevant Files

- `internal/pipes/calendar/calendar.go` — pattern for a deterministic pipe with an external client interface, flag resolution, and error classification
- `internal/pipes/calendar/cmd/main.go` — pattern for subprocess entry point with graceful degradation
- `internal/pipes/calendar/pipe.yaml` — pattern for deterministic pipe config with format templates and vocabulary
- `internal/pipes/memory/pipe.yaml` — pattern for a source pipe with vocabulary `sources` declaration and `action` flag
- `internal/pipes/draft/pipe.yaml` — defines the composition templates that consume source pipes (the `[verb, source]` and `[verb, type, source]` patterns the news pipe will plug into)
- `internal/envelope/envelope.go` — `Envelope`, `ContentList`, error helpers (`ClassifyError`, `FatalError`, `WarnError`)
- `internal/pipe/pipe.go` — `Handler` type signature, `Flag` struct
- `internal/pipehost/host.go` — `Run()`, `Fatal()`, `NewPipeLogger()`, `EnvUserDir`
- `internal/config/config.go` — `PipeConfig`, `VocabularyConfig` — vocabulary merge validates no word conflicts

### New Files

- `internal/pipes/news/pipe.yaml` — pipe definition: triggers, flags, vocabulary, format templates
- `internal/pipes/news/news.go` — handler implementation: source fetching, normalization, deduplication, collation
- `internal/pipes/news/news_test.go` — handler tests with mocked sources
- `internal/pipes/news/cmd/main.go` — subprocess entry point
- `internal/pipes/news/sources.go` — source types: RSS/Atom fetcher, JSON API fetcher, source config loading

## Implementation Plan

### Phase 1: Foundation

Define the data model — the `Article` struct that all sources normalize into, the `Source` interface that abstracts a fetcher, and the `SourceConfig` schema for declaring sources in `pipe.yaml`.

### Phase 2: Core Implementation

Build the RSS/Atom fetcher (covers the majority of news sources), the JSON API fetcher (for sources like Hacker News), and the handler that fans out to configured sources, deduplicates by URL, sorts by timestamp, and returns a collated list.

### Phase 3: Integration

Write the `pipe.yaml` with triggers, flags, vocabulary (declaring `news` as a source), and format templates. Write the `cmd/main.go` subprocess entry point. Verify composition with existing draft templates ("summarize the news" resolves correctly through planner).

## Step by Step Tasks

IMPORTANT: Execute every step in order, top to bottom.

### 1. Define the Article struct and Source interface

- Create `internal/pipes/news/news.go`
- Define the `Article` struct — the normalized item that all sources produce:
  ```go
  type Article struct {
      Title     string `json:"title"`
      Source    string `json:"source"`     // human-readable source name
      URL       string `json:"url"`
      Snippet   string `json:"snippet"`    // first ~200 chars or description
      Timestamp string `json:"timestamp"`  // RFC3339
  }
  ```
- Define the `Source` interface:
  ```go
  type Source interface {
      Name() string
      Fetch(ctx context.Context, limit int) ([]Article, error)
  }
  ```

### 2. Implement the RSS/Atom fetcher

- Create `internal/pipes/news/sources.go`
- Implement `RSSSource` that satisfies the `Source` interface
- Takes a URL and source name at construction
- Fetches and parses RSS 2.0 and Atom feeds using `encoding/xml` (no external dependency)
- Normalizes feed items into `Article` structs — map `<title>`, `<link>`, `<description>`/`<summary>`, and `<pubDate>`/`<updated>` to Article fields
- Truncate snippet to 200 characters
- Parse dates into RFC3339 format, falling back to the raw string if unparseable
- Timeout: use `context.Context` from the handler, respect the pipe's configured timeout

### 3. Implement the JSON API fetcher

- Add `JSONAPISource` to `sources.go`
- Takes a URL, source name, and field mapping at construction
- Field mapping tells the fetcher which JSON fields correspond to title, url, snippet, and timestamp — different APIs have different schemas
- Fetches the URL, decodes JSON, extracts articles using the field mapping
- Initial target: Hacker News API format (`/v0/topstories.json` → fetch individual items). Support can be expanded for other APIs later via the field mapping config
- For v1, a simpler approach: support a `jq`-style path config or just hard-code Hacker News as a built-in API source type with a `type: hackernews` discriminator

### 4. Define source configuration in pipe.yaml

- The pipe.yaml `flags` section defines runtime flags (topic, source, range, limit)
- Source configuration lives in a separate `sources` section within pipe.yaml — this is pipe-internal config, not a flag:
  ```yaml
  news_sources:
    - name: Hacker News
      type: rss
      url: https://hnrss.org/frontpage
    - name: Lobsters
      type: rss
      url: https://lobste.rs/rss
    - name: Go Blog
      type: rss
      url: https://go.dev/blog/feed.atom
  ```
- Load source config in `cmd/main.go` via `pipehost.LoadPipeConfig()` — the raw YAML is available as `PipeConfig` fields. Since `news_sources` is not a standard `PipeConfig` field, parse it from the raw YAML separately. Use a small helper that re-reads `pipe.yaml` and unmarshals only the `news_sources` key
- Alternatively, load sources from a `sources.yaml` file in the pipe's directory for cleaner separation. Decision: use a `sources.yaml` file alongside `pipe.yaml` — keeps pipe config clean and sources independently editable

### 5. Build the handler

- In `news.go`, implement `NewHandler(sources []Source, logger *slog.Logger) pipe.Handler`
- Follow the calendar handler pattern: `func(input envelope.Envelope, flags map[string]string) envelope.Envelope`
- Flag resolution:
  - `source` flag — if set, filter to only sources whose name matches (case-insensitive substring). If no sources match, return a fatal error
  - `topic` flag — if set, filter articles: check if topic appears in title or snippet (case-insensitive). This is a simple client-side filter, not a query parameter to the source
  - `range` flag — filter by timestamp. Values: `today`, `this-week`, `this-month`. Default: `today`. Parse article timestamps and exclude those outside the range
  - `limit` flag — maximum articles to return after all filtering. Default: `20`
- Fetch flow:
  1. Fan out to all matching sources concurrently using `sync.WaitGroup` + channel or `errgroup`
  2. Collect results into a single `[]Article` slice
  3. Deduplicate by URL (keep the first occurrence)
  4. Apply topic filter if set
  5. Apply range filter
  6. Sort by timestamp descending (newest first)
  7. Apply limit
- Error handling:
  - If all sources fail, return a fatal error with the combined error messages
  - If some sources fail, return the partial results with a `warn` error listing which sources failed
  - Classify individual source errors with `envelope.ClassifyError` — timeouts are retryable
- Return `content_type: list` with the `[]Article` slice as content

### 6. Write pipe.yaml

- Create `internal/pipes/news/pipe.yaml`:
  ```yaml
  name: news
  description: Fetches and collates news articles from configured sources.
  category: research
  timeout: 30s

  triggers:
    exact:
      - "what's in the news"
      - "check the news"
      - "show me the news"
    keywords:
      - news
      - headlines
      - articles
      - feed
    patterns:
      - "news about {topic}"
      - "what's in the news about {topic}"
      - "check the news {modifier}"

  flags:
    source:
      description: Filter to a specific news source by name.
      default: ""
    topic:
      description: Filter articles by topic keyword.
      default: ""
    range:
      description: Time range to fetch.
      values: [today, this-week, this-month]
      default: today
    limit:
      description: Maximum number of articles to return.
      default: "20"

  format:
    list: |
      {{if eq .Count 0}}No news found.{{else}}{{.Count}} article{{if gt .Count 1}}s{{end}}:{{range .Items}}
      - {{.title}} ({{.source}})
        {{.url}}{{end}}{{end}}

  vocabulary:
    verbs:
      headlines: news
    types: {}
    sources:
      news: news
      headlines: news
      feeds: news
      articles: news
    modifiers: {}

  templates:
    priority: 50
    entries:
      - requires: [verb, source, modifier]
        plan:
          - pipe: "{source}"
            flags: { range: "{modifier}" }
          - pipe: "{verb}"
            flags: { type: summary }

      - requires: [verb, source, topic]
        plan:
          - pipe: "{source}"
            flags: { topic: "{topic}" }
          - pipe: "{verb}"
            flags: {}
  ```

### 7. Write sources.yaml

- Create `internal/pipes/news/sources.yaml` with default sources:
  ```yaml
  sources:
    - name: Hacker News
      type: rss
      url: https://hnrss.org/frontpage
    - name: Lobsters
      type: rss
      url: https://lobste.rs/rss
  ```
- Write a `LoadSources(dir string) ([]SourceConfig, error)` function in `sources.go` that reads this file and returns the parsed configs
- `SourceConfig` struct:
  ```go
  type SourceConfig struct {
      Name string `yaml:"name"`
      Type string `yaml:"type"` // "rss" or "api"
      URL  string `yaml:"url"`
  }
  ```

### 8. Write cmd/main.go

- Create `internal/pipes/news/cmd/main.go`
- Follow the calendar pattern:
  ```go
  func main() {
      logger := pipehost.NewPipeLogger("news")

      // Load source configs from sources.yaml in pipe directory
      cwd, err := os.Getwd()
      if err != nil {
          pipehost.Fatal("news", fmt.Sprintf("getting working directory: %v", err))
      }

      configs, err := news.LoadSources(cwd)
      if err != nil {
          logger.Warn("no sources configured", "error", err)
          pipehost.Run(news.NewHandler(nil, logger), nil)
          return
      }

      sources := news.BuildSources(configs)
      logger.Info("initialized", "sources", len(sources))
      pipehost.Run(news.NewHandler(sources, logger), nil)
  }
  ```
- `BuildSources` converts `[]SourceConfig` into `[]Source` by creating `RSSSource` or `JSONAPISource` instances based on the `type` field

### 9. Write tests

- Create `internal/pipes/news/news_test.go`
- Test the handler with mocked sources (implement a `mockSource` that satisfies the `Source` interface and returns predetermined articles)
- Test cases:
  - **Happy path**: 2 mock sources, each returns 3 articles → handler returns 6 articles sorted by timestamp
  - **Deduplication**: two sources return an article with the same URL → handler returns it once
  - **Topic filtering**: set `topic` flag → only articles containing the topic in title or snippet are returned
  - **Range filtering**: set `range=today` → only articles from today are returned
  - **Limit**: set `limit=5` with 10 articles available → handler returns 5
  - **Source filter**: set `source` flag → only articles from the matching source are returned
  - **All sources fail**: both mock sources return errors → handler returns fatal error
  - **Partial failure**: one source fails, one succeeds → handler returns partial results with warn error
  - **No sources configured**: `NewHandler(nil, logger)` → returns fatal error
  - **Empty results**: sources return empty slices → handler returns empty list (not error)
  - **Envelope compliance**: output has all required fields populated (pipe, action, args, timestamp, duration, content, content_type, error)

### 10. Verify composition with existing templates

- Manually trace through the planner to verify these signals resolve correctly:
  - "what's in the news" → router hits `news` via exact match → planner: no template match (only verb) → single step: `news` pipe with no flags → returns article list
  - "summarize the news" → parser: verb=`summarize`→`draft`, source=`news`→`news` → draft's template `[verb, source]` matches → plan: `news.retrieve` → `draft` → draft summarizes the article list
  - "news about Go" → router hits `news` via keyword → parser: topic=`Go` → single step: `news` with `topic=Go`
  - "summarize the news about Go" → parser: verb=`summarize`→`draft`, source=`news`→`news`, topic=`Go` → draft's template `[verb, source]` matches → plan: `news(topic=Go)` → `draft`
- Verify no vocabulary conflicts with existing pipes by checking all words in `verbs` and `sources` against existing pipe.yaml files:
  - `headlines` as a verb: not used by any existing pipe
  - `news`, `headlines`, `feeds`, `articles` as sources: not used by any existing pipe

## Testing Strategy

### Unit Tests

- `internal/pipes/news/news_test.go` — all test cases listed in Step 9
- Tests use mock sources, no network calls
- Each test constructs an input envelope with specific flags and asserts on the output envelope's content, content_type, and error fields

### Edge Cases

- Article with unparseable timestamp — should not crash, use empty string or raw value
- Article with missing title or URL — should be included but render gracefully in format template
- Source that returns thousands of articles — limit flag caps output
- Source URL that redirects — HTTP client should follow redirects (default behavior)
- Feed with invalid XML — source should return error, handler degrades gracefully
- Duplicate articles across 3+ sources — dedup by URL keeps only the first occurrence
- Unicode in article titles and snippets — should pass through unmodified
- Empty topic filter string — should match all articles (no filtering)

## Risk Assessment

- **Vocabulary conflicts**: The words `news`, `headlines`, `feeds`, `articles` must not conflict with any existing pipe's vocabulary. Verified against current pipe.yaml files — no conflicts exist. Future pipes adding these words would get a startup error from the config loader's conflict detection.
- **Network dependency**: The pipe depends on external HTTP requests. All source fetches use `context.Context` with the pipe's configured timeout. Partial failure is handled gracefully. This is the same pattern as the calendar pipe.
- **No new dependencies**: RSS/Atom parsing uses `encoding/xml` from the standard library. No new go.mod entries needed.
- **Source config file**: Adding `sources.yaml` is a new pattern (existing pipes only have `pipe.yaml`). This is a clean separation but means the build/discovery system doesn't need to know about it — the pipe's `cmd/main.go` loads it directly from its working directory (which `pipehost` sets to the pipe's folder).

## Validation Commands

The build skill runs these commands as its final validation step before reporting.

```
just build
just test
just lint
```

## Open Questions (Unresolved)

1. **Should source config live in `sources.yaml` or in `pipe.yaml` under a custom key?**
   The spec proposes `sources.yaml` for cleaner separation. Alternative: add a `news_sources` key to `pipe.yaml` and parse it with a custom unmarshal step. Recommendation: `sources.yaml` — it's independently editable and avoids polluting the standard `PipeConfig` schema with pipe-specific fields. The tradeoff is a new file pattern, but it's self-contained within the pipe's directory.

2. **Should the pipe support user-configurable sources via `~/.config/virgil/news-sources.yaml`?**
   The spec currently defines sources in the pipe's own directory (part of the codebase). For user customization, a file in `VIRGIL_USER_DIR` would let users add their own feeds without modifying the codebase. Recommendation: support both — load defaults from the pipe directory, then merge/override with a user config file if it exists. Implement the merge in a follow-up task to keep v1 focused.

3. **Should the RSS fetcher support authenticated feeds?**
   Some RSS feeds require authentication (e.g., private Feedbin feeds). Recommendation: defer. Start with public feeds only. Authentication can be added later via per-source credentials in the source config.

## Sub-Tasks

Single task — no decomposition needed. The pipe is self-contained with no cross-cutting changes to existing code. All new files are within `internal/pipes/news/`.
