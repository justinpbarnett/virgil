package study

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipeutil"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/webscrape"
)

// Config holds the dependencies for the study pipe handler.
type Config struct {
	Provider   bridge.Provider    // may be nil — AI compression disabled
	Store      *store.Store       // for memory source
	WorkDir    string             // workspace root for codebase/file sources
	Fetcher    *webscrape.Fetcher // for web source; nil disables web
	Searcher   webscrape.Searcher // for web query discovery; nil = URL-only mode
	PipeConfig config.PipeConfig
	Logger     *slog.Logger
}

// NewHandler creates the study pipe handler.
func NewHandler(cfg Config) pipe.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("study", "gather")
		out.Args = flags

		source := pipeutil.FlagOrDefault(flags, "source", "codebase")
		role := pipeutil.FlagOrDefault(flags, "role", "general")
		depth := pipeutil.FlagOrDefault(flags, "depth", "normal")
		entry := flags["entry"]
		compression := pipeutil.FlagOrDefault(flags, "compression", "ai")

		budget := 8000
		if b, err := strconv.Atoi(flags["budget"]); err == nil && b > 0 {
			budget = b
		}

		query := envelope.ContentToText(input.Content, input.ContentType)
		if query == "" && entry == "" {
			out.Error = envelope.FatalError("no query or entry point provided")
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		ctx := context.Background()
		maxTier := maxCompressionTier(compression)
		systemPrompt := cfg.PipeConfig.Prompts.System

		result, err := runStudy(ctx, studyParams{
			query:        query,
			source:       source,
			role:         role,
			depth:        depth,
			entry:        entry,
			budget:       budget,
			maxTier:      maxTier,
			systemPrompt: systemPrompt,
			provider:     cfg.Provider,
			store:        cfg.Store,
			workDir:      cfg.WorkDir,
			fetcher:      cfg.Fetcher,
			searcher:     cfg.Searcher,
			logger:       logger,
		})
		if err != nil {
			out.Error = envelope.ClassifyError("study", err)
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		out.Content = result
		out.ContentType = envelope.ContentStructured
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

type studyParams struct {
	query        string
	source       string
	role         string
	depth        string
	entry        string
	budget       int
	maxTier      int
	systemPrompt string
	provider     bridge.Provider
	store        *store.Store
	workDir      string
	fetcher      *webscrape.Fetcher
	searcher     webscrape.Searcher
	logger       *slog.Logger
}

func runStudy(ctx context.Context, p studyParams) (*StudyOutput, error) {
	var timing Timing

	// Select source backend
	backend, err := selectBackend(p)
	if err != nil {
		return nil, err
	}

	// Stage 1: Discover
	discoverStart := time.Now()
	candidates, err := backend.Discover(ctx, p.query, p.entry, p.depth)
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	// Track discovery source agreement before dedup (multiple methods finding same item)
	sourceAgreement := buildSourceAgreement(candidates)

	candidates = deduplicateCandidates(candidates)
	timing.DiscoverMs = time.Since(discoverStart).Milliseconds()

	p.logger.Info("discover complete",
		"candidates", len(candidates),
		"source", p.source,
		"ms", timing.DiscoverMs,
	)

	if len(candidates) == 0 {
		return &StudyOutput{
			Summary: "No relevant content found.",
			Metadata: OutputMetadata{
				Source: p.source,
				Entry:  p.entry,
				Role:   p.role,
				Depth:  p.depth,
				Timing: timing,
			},
		}, nil
	}

	// Build discovery stats
	discoveryStats := buildDiscoveryStats(candidates)

	// Stage 2: Extract
	extractStart := time.Now()
	items, err := backend.Extract(ctx, candidates, p.role)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	timing.ExtractMs = time.Since(extractStart).Milliseconds()

	p.logger.Info("extract complete",
		"items", len(items),
		"ms", timing.ExtractMs,
	)

	if len(items) == 0 {
		return &StudyOutput{
			Summary: "Candidates found but no content could be extracted.",
			Metadata: OutputMetadata{
				Source:    p.source,
				Entry:     p.entry,
				Role:      p.role,
				Depth:     p.depth,
				Discovery: discoveryStats,
				Timing:    timing,
			},
		}, nil
	}

	// Stage 3: Rank
	rankStart := time.Now()
	ranked := rankItems(items, p.query, p.role, sourceAgreement)
	timing.RankMs = time.Since(rankStart).Milliseconds()

	p.logger.Info("rank complete",
		"ranked", len(ranked),
		"ms", timing.RankMs,
	)

	// Stage 4: Compress
	compressStart := time.Now()
	compressed := compress(ctx, ranked, p.budget, p.maxTier, p.provider, p.systemPrompt, p.logger)
	timing.CompressMs = time.Since(compressStart).Milliseconds()

	p.logger.Info("compress complete",
		"included", compressed.Coverage.IncludedItems,
		"excluded", compressed.Coverage.ExcludedItems,
		"tier", compressed.Coverage.CompressionTier,
		"budget_used", compressed.Coverage.BudgetUsed,
		"ms", timing.CompressMs,
	)

	// Build output
	summary := fmt.Sprintf(
		"Context gathered from %s for %s role. Entry point: %s. %d items included across %d files. Coverage: %.0f%% of relevance score within budget.",
		p.source, p.role, entryDescription(p.entry),
		compressed.Coverage.IncludedItems, countUniquePaths(compressed.Items),
		compressed.Coverage.IncludedScoreMass*100,
	)

	return &StudyOutput{
		Summary:     summary,
		Items:       compressed.Items,
		AISummaries: compressed.AISummaries,
		Metadata: OutputMetadata{
			Source:         p.source,
			Entry:          p.entry,
			Role:           p.role,
			Depth:          p.depth,
			TokenEstimated: tokenEstimated,
			Coverage:       compressed.Coverage,
			Discovery:      discoveryStats,
			Timing:         timing,
		},
	}, nil
}

func selectBackend(p studyParams) (SourceBackend, error) {
	switch p.source {
	case "codebase":
		if p.workDir == "" {
			return nil, fmt.Errorf("no workspace directory configured")
		}
		return newCodebaseBackend(p.workDir, p.logger), nil

	case "memory":
		if p.store == nil {
			return nil, fmt.Errorf("no memory store configured")
		}
		return newMemoryBackend(p.store, p.logger), nil

	case "files":
		if p.workDir == "" {
			return nil, fmt.Errorf("no workspace directory configured")
		}
		return newFileBackend(p.workDir, p.logger), nil

	case "web":
		if p.fetcher == nil {
			return nil, fmt.Errorf("web source requires an HTTP fetcher (not configured)")
		}
		return newWebBackend(p.fetcher, p.searcher, p.logger), nil

	default:
		return nil, fmt.Errorf("unsupported source: %s", p.source)
	}
}

func buildSourceAgreement(candidates []Candidate) map[string]int {
	counts := make(map[string]int)
	for _, c := range candidates {
		counts[c.ID]++
	}
	return counts
}

func buildDiscoveryStats(candidates []Candidate) DiscoveryStats {
	stats := DiscoveryStats{DeduplicatedTotal: len(candidates)}
	for _, c := range candidates {
		switch c.Source {
		case "text":
			stats.TextCandidates++
		case "index", "file":
			stats.IndexCandidates++
		case "lsp":
			stats.LSPCandidates++
		case "memory":
			stats.IndexCandidates++
		case "web":
			stats.WebCandidates++
		}
	}
	return stats
}

func entryDescription(entry string) string {
	if entry == "" {
		return "(query-based)"
	}
	return entry
}

func countUniquePaths(items []OutputItem) int {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[item.Path] = struct{}{}
	}
	return len(seen)
}

// SearchCodebase runs a structural codebase search for the given query and
// returns results as MemoryEntry values ready for injection into an envelope.
// It uses TierElision compression (no AI) for speed.
func SearchCodebase(ctx context.Context, query, workDir string, budget int) ([]envelope.MemoryEntry, error) {
	result, err := runStudy(ctx, studyParams{
		query:   query,
		source:  "codebase",
		role:    "planner",
		depth:   "normal",
		budget:  budget,
		maxTier: TierElision,
		workDir: workDir,
		logger:  slog.Default(),
	})
	if err != nil {
		return nil, err
	}

	var entries []envelope.MemoryEntry
	for _, item := range result.Items {
		entries = append(entries, envelope.MemoryEntry{
			ID:      item.Path + ":" + item.Symbol,
			Type:    "codebase",
			Content: item.Path + ":\n" + item.Content,
		})
	}
	for _, s := range result.AISummaries {
		entries = append(entries, envelope.MemoryEntry{
			ID:      "ai-summary:" + s.Theme,
			Type:    "codebase",
			Content: s.Content,
		})
	}
	return entries, nil
}
