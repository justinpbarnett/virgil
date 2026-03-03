package study

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/justinpbarnett/virgil/internal/bridge"
)

// CompressResult holds the output of the compression stage.
type CompressResult struct {
	Items       []OutputItem
	AISummaries []AISummary
	Coverage    Coverage
}

// compress fits ranked items into the token budget using layered compression.
// Tiers 0-2 are deterministic. Tier 3 uses the AI provider.
func compress(ctx context.Context, ranked []RankedItem, budget int, maxTier int, provider bridge.Provider, systemPrompt string, logger *slog.Logger) CompressResult {
	overhead := budget / 10 // reserve 10% for framing
	available := budget - overhead

	result := CompressResult{
		Coverage: Coverage{
			BudgetTotal: budget,
		},
	}

	if len(ranked) == 0 {
		return result
	}

	totalScoreMass := 0.0
	for _, r := range ranked {
		totalScoreMass += r.Score
	}

	// Tier 0: Greedy fill
	included, excluded, used := greedyFill(ranked, available)
	result.Items = toOutputItems(included)
	result.Coverage.BudgetUsed = used
	result.Coverage.CompressionTier = TierNone

	if len(excluded) == 0 || maxTier < TierDemotion {
		result.Coverage = buildCoverage(included, excluded, totalScoreMass, used, budget, TierNone, 0)
		return result
	}

	// Tier 1: Granularity demotion — demote included items to free space
	if maxTier >= TierDemotion {
		demoted, freed := demoteGranularity(included)
		if freed > 0 {
			// Try to fit more excluded items with freed space
			additional, stillExcluded, _ := greedyFill(excluded, freed)
			included = append(demoted, additional...)
			excluded = stillExcluded
			used = 0
			for _, item := range included {
				used += item.TokenCount
			}
			result.Items = toOutputItems(included)
			result.Coverage.BudgetUsed = used
			result.Coverage.CompressionTier = TierDemotion
		}
	}

	if len(excluded) == 0 || maxTier < TierElision {
		result.Coverage = buildCoverage(included, excluded, totalScoreMass, used, budget, TierDemotion, 0)
		return result
	}

	// Tier 2: Structural elision — strip inferrable content
	if maxTier >= TierElision {
		elided, freed := structuralElision(included)
		if freed > 0 {
			additional, stillExcluded, _ := greedyFill(excluded, freed)
			included = append(elided, additional...)
			excluded = stillExcluded
			used = 0
			for _, item := range included {
				used += item.TokenCount
			}
			result.Items = toOutputItems(included)
			result.Coverage.BudgetUsed = used
			result.Coverage.CompressionTier = TierElision
		}
	}

	if len(excluded) == 0 || maxTier < TierAI || provider == nil {
		tier := TierElision
		if maxTier < TierElision {
			tier = TierDemotion
		}
		result.Coverage = buildCoverage(included, excluded, totalScoreMass, used, budget, tier, 0)
		return result
	}

	// Tier 3: AI summarization of remaining excluded items
	remaining := budget - used
	if remaining > 50 {
		summaries := aiSummarize(ctx, excluded, remaining, provider, systemPrompt, logger)
		result.AISummaries = summaries
		summaryTokens := 0
		for _, s := range summaries {
			summaryTokens += s.Tokens
		}
		used += summaryTokens
		result.Coverage.BudgetUsed = used
		result.Coverage.CompressionTier = TierAI
		result.Coverage.AISummaries = len(summaries)
	}

	result.Coverage = buildCoverage(included, excluded, totalScoreMass, used, budget, TierAI, len(result.AISummaries))
	return result
}

// greedyFill takes items from the ranked list until the budget is exhausted.
func greedyFill(ranked []RankedItem, budget int) (included []RankedItem, excluded []RankedItem, used int) {
	for _, r := range ranked {
		if FitsInBudget(used, r.TokenCount, budget) {
			included = append(included, r)
			used += r.TokenCount
		} else {
			excluded = append(excluded, r)
		}
	}
	return
}

// demoteGranularity reduces the detail level of included items to free budget.
func demoteGranularity(items []RankedItem) (demoted []RankedItem, freed int) {
	for _, item := range items {
		switch item.Granularity {
		case GranDeclaration:
			// Demote to signature: keep first line + docstring-like lines
			compressed := extractSignature(item.Content)
			oldTokens := item.TokenCount
			newTokens := CountTokens(compressed)
			if newTokens < oldTokens {
				item.Content = compressed
				item.TokenCount = newTokens
				item.Granularity = GranSignature
				freed += oldTokens - newTokens
			}

		case GranBlock:
			// Demote to first/last N lines
			compressed := truncateBlock(item.Content, 5)
			oldTokens := item.TokenCount
			newTokens := CountTokens(compressed)
			if newTokens < oldTokens {
				item.Content = compressed
				item.TokenCount = newTokens
				freed += oldTokens - newTokens
			}
		}

		demoted = append(demoted, item)
	}
	return
}

// extractSignature extracts a function/method signature from a declaration.
// Keeps the first line (which typically contains the signature) plus any
// immediately preceding comment lines.
func extractSignature(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 3 {
		return content // already short enough
	}

	var sigLines []string
	foundSig := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !foundSig {
			// Collect comment/docstring lines and the first code line
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
				strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
				strings.HasPrefix(trimmed, "\"\"\"") || strings.HasPrefix(trimmed, "///") {
				sigLines = append(sigLines, line)
				continue
			}
			sigLines = append(sigLines, line)
			foundSig = true
			// If this line has an opening brace, we have the signature
			if strings.Contains(line, "{") || strings.Contains(line, ":") {
				break
			}
		} else {
			// Continue collecting signature lines until opening brace
			sigLines = append(sigLines, line)
			if strings.Contains(line, "{") || strings.Contains(line, ":") {
				break
			}
			if len(sigLines) > 5 {
				break
			}
		}
	}

	return strings.Join(sigLines, "\n")
}

// truncateBlock keeps the first and last N lines of a block, with an elision marker.
func truncateBlock(content string, n int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= n*2+1 {
		return content
	}

	head := lines[:n]
	tail := lines[len(lines)-n:]
	omitted := len(lines) - 2*n

	var result []string
	result = append(result, head...)
	result = append(result, fmt.Sprintf("    // ... %d lines omitted ...", omitted))
	result = append(result, tail...)
	return strings.Join(result, "\n")
}

// structuralElision removes content that is inferrable from context.
func structuralElision(items []RankedItem) (elided []RankedItem, freed int) {
	for _, item := range items {
		content := item.Content

		// Collapse import blocks
		content = collapseImports(content)

		// Remove non-doc comments
		content = stripComments(content)

		newTokens := CountTokens(content)
		if newTokens < item.TokenCount {
			freed += item.TokenCount - newTokens
			item.Content = content
			item.TokenCount = newTokens
		}

		elided = append(elided, item)
	}
	return
}

// collapseImports replaces multi-line import blocks with a compact summary.
func collapseImports(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inImport := false
	var importPkgs []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Go import block
		if trimmed == "import (" {
			inImport = true
			importPkgs = nil
			continue
		}
		if inImport {
			if trimmed == ")" {
				inImport = false
				if len(importPkgs) > 0 {
					result = append(result, fmt.Sprintf("// imports: %s", strings.Join(importPkgs, ", ")))
				}
				continue
			}
			pkg := strings.Trim(trimmed, "\"")
			if pkg != "" {
				// Take just the last path segment
				parts := strings.Split(pkg, "/")
				importPkgs = append(importPkgs, parts[len(parts)-1])
			}
			continue
		}

		// Python imports
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") {
			// Keep these as-is but could collapse later
			result = append(result, line)
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// stripComments removes single-line comments that aren't doc comments.
func stripComments(content string) string {
	lines := strings.Split(content, "\n")
	var result []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Keep doc comments (comments immediately before a code line)
		isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")

		if isComment {
			// Check if next line is code (making this a doc comment)
			if i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				isNextCode := nextTrimmed != "" &&
					!strings.HasPrefix(nextTrimmed, "//") &&
					!strings.HasPrefix(nextTrimmed, "#")
				if isNextCode {
					result = append(result, line) // keep doc comments
					continue
				}
			}
			// Skip non-doc comments
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// aiSummarize uses the provider to create dense summaries of excluded items.
func aiSummarize(ctx context.Context, excluded []RankedItem, tokenBudget int, provider bridge.Provider, systemPrompt string, logger *slog.Logger) []AISummary {
	if len(excluded) == 0 || tokenBudget < 50 {
		return nil
	}

	// Group items by directory/theme
	groups := groupByTheme(excluded)

	var summaries []AISummary
	budgetPerGroup := tokenBudget / max(len(groups), 1)

	for theme, items := range groups {
		if ctx.Err() != nil {
			break
		}

		prompt := buildSummarizePrompt(items, budgetPerGroup)
		if prompt == "" {
			continue
		}

		result, err := provider.Complete(ctx, systemPrompt, prompt)
		if err != nil {
			logger.Warn("ai summarization failed", "theme", theme, "error", err)
			continue
		}

		var covers []string
		for _, item := range items {
			covers = append(covers, item.Metadata.Path)
		}

		summary := AISummary{
			Theme:   theme,
			Covers:  covers,
			Tokens:  CountTokens(result),
			Content: result,
		}
		summaries = append(summaries, summary)
	}

	return summaries
}

func groupByTheme(items []RankedItem) map[string][]RankedItem {
	groups := make(map[string][]RankedItem)
	for _, item := range items {
		// Group by directory
		dir := item.Metadata.Path
		if idx := strings.LastIndex(dir, "/"); idx >= 0 {
			dir = dir[:idx]
		}
		if dir == "" {
			dir = "root"
		}
		groups[dir] = append(groups[dir], item)
	}
	return groups
}

var summarizeTemplate = template.Must(template.New("summarize").Parse(`Summarize the following {{.ItemCount}} code items into a dense paragraph of approximately {{.TargetTokens}} tokens.

Preserve: type names, function signatures, interface definitions, key constants, error types, and dependency relationships.

Remove: implementation details, comments, formatting, boilerplate.

Items:
{{range .Items}}--- {{.Path}} ({{.Symbol}}, {{.Kind}}) ---
{{.Content}}
{{end}}
Produce a single dense paragraph. No headers, no bullets, no code blocks. Just information-dense prose that a developer can read to understand what these components do and how they relate.`))

type summarizeData struct {
	ItemCount    int
	TargetTokens int
	Items        []summarizeItem
}

type summarizeItem struct {
	Path    string
	Symbol  string
	Kind    string
	Content string
}

func buildSummarizePrompt(items []RankedItem, targetTokens int) string {
	data := summarizeData{
		ItemCount:    len(items),
		TargetTokens: targetTokens,
	}
	for _, item := range items {
		data.Items = append(data.Items, summarizeItem{
			Path:    item.Metadata.Path,
			Symbol:  item.Metadata.Symbol,
			Kind:    item.Metadata.Kind,
			Content: item.Content,
		})
	}

	var buf bytes.Buffer
	if err := summarizeTemplate.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

func toOutputItems(ranked []RankedItem) []OutputItem {
	items := make([]OutputItem, 0, len(ranked))
	for _, r := range ranked {
		items = append(items, OutputItem{
			Path:        r.Metadata.Path,
			Symbol:      r.Metadata.Symbol,
			Kind:        r.Metadata.Kind,
			Granularity: r.Granularity,
			Distance:    r.Metadata.Distance,
			Tokens:      r.TokenCount,
			Content:     r.Content,
		})
	}
	return items
}

func buildCoverage(included, excluded []RankedItem, totalScoreMass float64, used, budget, tier, aiSummaries int) Coverage {
	includedMass := 0.0
	for _, r := range included {
		includedMass += r.Score
	}

	scoreMassRatio := 0.0
	if totalScoreMass > 0 {
		scoreMassRatio = includedMass / totalScoreMass
	}

	return Coverage{
		IncludedItems:     len(included),
		ExcludedItems:     len(excluded),
		IncludedScoreMass: scoreMassRatio,
		CompressionTier:   tier,
		BudgetUsed:        used,
		BudgetTotal:       budget,
		AISummaries:       aiSummaries,
	}
}
