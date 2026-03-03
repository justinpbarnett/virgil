package study

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/store"
)

// maxCandidatesPerMethod caps how many candidates a single discovery method returns.
const maxCandidatesPerMethod = 50

// maxTotalCandidates caps the deduplicated total across all methods.
const maxTotalCandidates = 100

// SourceBackend discovers and extracts content from a specific source type.
type SourceBackend interface {
	Discover(ctx context.Context, query, entry, depth string) ([]Candidate, error)
	Extract(ctx context.Context, candidates []Candidate, role string) ([]ExtractedItem, error)
}

// codebaseBackend discovers content via text search across a directory tree.
// When LSP and tree-sitter become available, this will be extended with
// semantic discovery and AST-aware extraction.
type codebaseBackend struct {
	root   string
	logger *slog.Logger
}

func newCodebaseBackend(root string, logger *slog.Logger) *codebaseBackend {
	return &codebaseBackend{root: root, logger: logger}
}

func (b *codebaseBackend) Discover(ctx context.Context, query, entry, depth string) ([]Candidate, error) {
	if entry == "" && query == "" {
		return nil, nil
	}

	searchRoot := b.root
	if entry != "" {
		abs := entry
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(b.root, entry)
		}
		info, err := os.Stat(abs)
		if err == nil && info.IsDir() {
			searchRoot = abs
		}
	}

	searchTerms := query
	if searchTerms == "" {
		searchTerms = filepath.Base(entry)
	}

	var candidates []Candidate
	terms := strings.Fields(strings.ToLower(searchTerms))

	err := filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSearchableFile(d.Name()) {
			return nil
		}
		if len(candidates) >= maxCandidatesPerMethod {
			return filepath.SkipAll
		}

		relPath, _ := filepath.Rel(b.root, path)
		if relPath == "" {
			relPath = path
		}

		// Check filename match first
		lowerName := strings.ToLower(d.Name())
		nameMatch := false
		for _, term := range terms {
			if strings.Contains(lowerName, term) {
				nameMatch = true
				break
			}
		}

		// Scan file content for matches
		matches := b.scanFile(path, terms)

		if !nameMatch && len(matches) == 0 {
			return nil
		}

		if len(matches) > 0 {
			for _, m := range matches {
				if len(candidates) >= maxCandidatesPerMethod {
					break
				}
				id := fmt.Sprintf("%s:%d", relPath, m.line)
				candidates = append(candidates, Candidate{
					ID:       id,
					Source:   "text",
					Location: Location{Path: relPath, StartLine: m.line, EndLine: m.line, Symbol: ""},
					Preview:  m.text,
					SizeHint: CountTokens(m.text) * 10, // rough estimate for surrounding context
				})
			}
		} else if nameMatch {
			candidates = append(candidates, Candidate{
				ID:       relPath,
				Source:   "index",
				Location: Location{Path: relPath},
				Preview:  relPath,
				SizeHint: estimateFileTokens(path),
			})
		}

		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return candidates, err
	}

	return candidates, nil
}

type lineMatch struct {
	line int
	text string
}

func (b *codebaseBackend) scanFile(path string, terms []string) []lineMatch {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var matches []lineMatch
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		lower := strings.ToLower(line)
		for _, term := range terms {
			if strings.Contains(lower, term) {
				matches = append(matches, lineMatch{line: lineNum, text: strings.TrimSpace(line)})
				break
			}
		}
		if len(matches) >= 20 { // cap per-file matches
			break
		}
	}
	return matches
}

func (b *codebaseBackend) Extract(ctx context.Context, candidates []Candidate, role string) ([]ExtractedItem, error) {
	var items []ExtractedItem

	// Group candidates by file to avoid re-reading
	byFile := make(map[string][]Candidate)
	for _, c := range candidates {
		byFile[c.Location.Path] = append(byFile[c.Location.Path], c)
	}

	for filePath, fileCandidates := range byFile {
		if ctx.Err() != nil {
			return items, ctx.Err()
		}

		absPath := filePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(b.root, filePath)
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			b.logger.Debug("skipping unreadable file", "path", filePath, "error", err)
			continue
		}

		lines := strings.Split(string(content), "\n")
		lang := languageFromPath(filePath)
		info, _ := os.Stat(absPath)
		var modTime time.Time
		if info != nil {
			modTime = info.ModTime()
		}

		for _, c := range fileCandidates {
			item := b.extractCandidate(c, lines, filePath, lang, modTime, role)
			if item.Content != "" {
				items = append(items, item)
			}
		}
	}

	return items, nil
}

func (b *codebaseBackend) extractCandidate(c Candidate, lines []string, filePath, lang string, modTime time.Time, role string) ExtractedItem {
	totalLines := len(lines)

	// For file-level candidates (from index), extract based on role
	if c.Location.StartLine == 0 && c.Location.EndLine == 0 {
		return b.extractFileByRole(c, lines, filePath, lang, modTime, role)
	}

	// For line-level candidates (from text search), extract based on role
	profile := extractionProfileForRole(role)
	startLine := c.Location.StartLine - profile.contextRadius
	if startLine < 1 {
		startLine = 1
	}
	endLine := c.Location.StartLine + profile.contextRadius
	if endLine > totalLines {
		endLine = totalLines
	}

	extracted := strings.Join(lines[startLine-1:endLine], "\n")
	granularity := GranBlock
	kind := KindBlock

	// For reviewer/planner roles, try to reduce to signature if the match
	// looks like a function/type declaration
	if profile.preferSignatures {
		matchLine := lines[c.Location.StartLine-1]
		trimmed := strings.TrimSpace(matchLine)
		if looksLikeDeclaration(trimmed, lang) {
			sig := extractSignatureFromLines(lines, c.Location.StartLine-1)
			if sig != "" {
				extracted = sig
				granularity = GranSignature
				kind = KindFunction
			}
		}
	}

	return ExtractedItem{
		ID:          c.ID,
		Content:     extracted,
		Granularity: granularity,
		TokenCount:  CountTokens(extracted),
		Metadata: ItemMetadata{
			Language: lang,
			Path:     filePath,
			Lines:    [2]int{startLine, endLine},
			Symbol:   c.Location.Symbol,
			Kind:     kind,
			Distance: 0,
			Modified: modTime,
		},
	}
}

func (b *codebaseBackend) extractFileByRole(c Candidate, lines []string, filePath, lang string, modTime time.Time, role string) ExtractedItem {
	profile := extractionProfileForRole(role)

	endLine := profile.fileSummaryLines
	if endLine > len(lines) {
		endLine = len(lines)
	}
	extracted := strings.Join(lines[:endLine], "\n")

	return ExtractedItem{
		ID:          c.ID,
		Content:     extracted,
		Granularity: profile.fileGranularity,
		TokenCount:  CountTokens(extracted),
		Metadata: ItemMetadata{
			Language: lang,
			Path:     filePath,
			Lines:    [2]int{1, endLine},
			Kind:     KindFile,
			Distance: 0,
			Modified: modTime,
		},
	}
}

// extractionProfile defines how extraction behaves for a given role.
type extractionProfile struct {
	contextRadius    int
	preferSignatures bool   // true for reviewer/planner: reduce declarations to signatures
	fileSummaryLines int    // how many lines to include in file-level extractions
	fileGranularity  string // granularity tag for file-level extractions
}

func extractionProfileForRole(role string) extractionProfile {
	switch role {
	case "builder":
		return extractionProfile{
			contextRadius:    20,
			preferSignatures: false,
			fileSummaryLines: 50, // more detail: imports + types + functions
			fileGranularity:  GranDeclaration,
		}
	case "debugger":
		return extractionProfile{
			contextRadius:    25,
			preferSignatures: false,
			fileSummaryLines: 40,
			fileGranularity:  GranDeclaration,
		}
	case "reviewer":
		return extractionProfile{
			contextRadius:    15,
			preferSignatures: true,
			fileSummaryLines: 30, // contracts: package + interfaces + type signatures
			fileGranularity:  GranInterface,
		}
	case "planner":
		return extractionProfile{
			contextRadius:    10,
			preferSignatures: true,
			fileSummaryLines: 20, // architecture: package decl + imports + exported types
			fileGranularity:  GranFileSummary,
		}
	default: // "general"
		return extractionProfile{
			contextRadius:    15,
			preferSignatures: false,
			fileSummaryLines: 30,
			fileGranularity:  GranFileSummary,
		}
	}
}

// looksLikeDeclaration checks if a line appears to be a function/type/method declaration.
func looksLikeDeclaration(line, lang string) bool {
	switch lang {
	case "go":
		return strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "type ")
	case "python":
		return strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "class ")
	case "javascript", "typescript":
		return strings.HasPrefix(line, "function ") || strings.HasPrefix(line, "class ") ||
			strings.Contains(line, "=> {") || strings.HasPrefix(line, "export ")
	case "rust":
		return strings.HasPrefix(line, "fn ") || strings.HasPrefix(line, "pub fn ") ||
			strings.HasPrefix(line, "struct ") || strings.HasPrefix(line, "impl ")
	case "java", "kotlin":
		return strings.Contains(line, "class ") || strings.Contains(line, "interface ") ||
			(strings.Contains(line, "(") && strings.Contains(line, ")"))
	default:
		return strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "def ") ||
			strings.HasPrefix(line, "function ") || strings.HasPrefix(line, "class ")
	}
}

// extractSignatureFromLines extracts a function/type signature starting at lineIdx.
// Returns the signature lines up to (but not including) the body.
func extractSignatureFromLines(lines []string, lineIdx int) string {
	if lineIdx >= len(lines) {
		return ""
	}

	var sigLines []string
	// Collect preceding doc comments
	for i := lineIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "///") || strings.HasPrefix(trimmed, "\"\"\"") {
			sigLines = append([]string{lines[i]}, sigLines...)
		} else {
			break
		}
	}

	// Collect the declaration line(s)
	for i := lineIdx; i < len(lines) && i < lineIdx+5; i++ {
		sigLines = append(sigLines, lines[i])
		if strings.Contains(lines[i], "{") || strings.Contains(lines[i], ":") {
			break
		}
	}

	return strings.Join(sigLines, "\n")
}

// memoryBackend discovers content from Virgil's memory store.
type memoryBackend struct {
	store  *store.Store
	logger *slog.Logger
}

func newMemoryBackend(s *store.Store, logger *slog.Logger) *memoryBackend {
	return &memoryBackend{store: s, logger: logger}
}

func (b *memoryBackend) Discover(_ context.Context, query, entry, _ string) ([]Candidate, error) {
	searchQuery := query
	if searchQuery == "" {
		searchQuery = entry
	}
	if searchQuery == "" {
		return nil, nil
	}

	entries, err := b.store.Search(searchQuery, maxCandidatesPerMethod, "")
	if err != nil {
		return nil, fmt.Errorf("memory search: %w", err)
	}

	var candidates []Candidate
	for _, e := range entries {
		id := fmt.Sprintf("memory:%d", e.ID)
		preview := e.Content
		if len(preview) > 100 {
			preview = preview[:100]
		}
		candidates = append(candidates, Candidate{
			ID:       id,
			Source:   "memory",
			Location: Location{Path: fmt.Sprintf("memory:%d", e.ID)},
			Preview:  preview,
			SizeHint: CountTokens(e.Content),
		})
	}

	return candidates, nil
}

func (b *memoryBackend) Extract(_ context.Context, candidates []Candidate, _ string) ([]ExtractedItem, error) {
	// Memory entries are already discrete items — just wrap them
	searchQuery := ""
	for _, c := range candidates {
		if c.Preview != "" {
			searchQuery = c.Preview
			break
		}
	}

	entries, err := b.store.Search(searchQuery, len(candidates), "")
	if err != nil {
		return nil, fmt.Errorf("memory extract: %w", err)
	}

	entryMap := make(map[int64]store.Entry)
	for _, e := range entries {
		entryMap[e.ID] = e
	}

	var items []ExtractedItem
	for _, c := range candidates {
		// Parse memory ID
		var memID int64
		fmt.Sscanf(c.Location.Path, "memory:%d", &memID)

		e, ok := entryMap[memID]
		if !ok {
			continue
		}

		items = append(items, ExtractedItem{
			ID:          c.ID,
			Content:     e.Content,
			Granularity: GranBlock,
			TokenCount:  CountTokens(e.Content),
			Metadata: ItemMetadata{
				Path:     c.Location.Path,
				Kind:     KindBlock,
				Distance: 0,
				Modified: e.UpdatedAt,
			},
		})
	}

	return items, nil
}

// fileBackend discovers content from the filesystem via filename matching and text search.
type fileBackend struct {
	root   string
	logger *slog.Logger
}

func newFileBackend(root string, logger *slog.Logger) *fileBackend {
	return &fileBackend{root: root, logger: logger}
}

func (b *fileBackend) Discover(ctx context.Context, query, entry, _ string) ([]Candidate, error) {
	searchRoot := b.root
	if entry != "" {
		abs := entry
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(b.root, entry)
		}
		if info, err := os.Stat(abs); err == nil {
			if info.IsDir() {
				searchRoot = abs
			} else {
				// Single file
				relPath, _ := filepath.Rel(b.root, abs)
				return []Candidate{{
					ID:       relPath,
					Source:   "file",
					Location: Location{Path: relPath},
					Preview:  relPath,
					SizeHint: estimateFileTokens(abs),
				}}, nil
			}
		}
	}

	var candidates []Candidate
	terms := strings.Fields(strings.ToLower(query))

	err := filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(candidates) >= maxCandidatesPerMethod {
			return filepath.SkipAll
		}

		relPath, _ := filepath.Rel(b.root, path)
		if relPath == "" {
			relPath = path
		}

		// Match by filename or content
		lowerName := strings.ToLower(d.Name())
		matched := len(terms) == 0 // if no query, include all files
		for _, term := range terms {
			if strings.Contains(lowerName, term) {
				matched = true
				break
			}
		}

		if matched {
			candidates = append(candidates, Candidate{
				ID:       relPath,
				Source:   "file",
				Location: Location{Path: relPath},
				Preview:  relPath,
				SizeHint: estimateFileTokens(path),
			})
		}

		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return candidates, err
	}

	return candidates, nil
}

func (b *fileBackend) Extract(ctx context.Context, candidates []Candidate, _ string) ([]ExtractedItem, error) {
	var items []ExtractedItem

	for _, c := range candidates {
		if ctx.Err() != nil {
			return items, ctx.Err()
		}

		absPath := c.Location.Path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(b.root, c.Location.Path)
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			b.logger.Debug("skipping unreadable file", "path", c.Location.Path, "error", err)
			continue
		}

		text := string(content)
		info, _ := os.Stat(absPath)
		var modTime time.Time
		if info != nil {
			modTime = info.ModTime()
		}

		items = append(items, ExtractedItem{
			ID:          c.ID,
			Content:     text,
			Granularity: GranBlock,
			TokenCount:  CountTokens(text),
			Metadata: ItemMetadata{
				Path:     c.Location.Path,
				Kind:     KindFile,
				Distance: 0,
				Modified: modTime,
			},
		})
	}

	return items, nil
}

// deduplicateCandidates removes candidates with the same ID, keeping the first occurrence.
func deduplicateCandidates(candidates []Candidate) []Candidate {
	seen := make(map[string]struct{}, len(candidates))
	var result []Candidate
	for _, c := range candidates {
		if _, dup := seen[c.ID]; dup {
			continue
		}
		seen[c.ID] = struct{}{}
		result = append(result, c)
	}
	if len(result) > maxTotalCandidates {
		result = result[:maxTotalCandidates]
	}
	return result
}

var skipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, ".cache": true,
	"__pycache__": true, ".tox": true, ".mypy_cache": true,
	"dist": true, "build": true, "target": true,
	".idea": true, ".vscode": true,
}

func shouldSkipDir(name string) bool {
	return skipDirs[name]
}

var searchableExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".rs": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true,
	".rb": true, ".php": true, ".swift": true, ".kt": true, ".scala": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".md": true, ".txt": true, ".cfg": true, ".ini": true,
	".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".graphql": true, ".proto": true,
	".html": true, ".css": true, ".scss": true,
	".xml": true, ".svg": true,
}

func isSearchableFile(name string) bool {
	return searchableExts[strings.ToLower(filepath.Ext(name))]
}

var extToLang = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".ts": "typescript",
	".tsx": "typescript", ".jsx": "javascript", ".rs": "rust", ".java": "java",
	".c": "c", ".h": "c", ".cpp": "cpp", ".hpp": "cpp",
	".rb": "ruby", ".php": "php", ".swift": "swift", ".kt": "kotlin",
	".scala": "scala", ".yaml": "yaml", ".yml": "yaml", ".json": "json",
	".toml": "toml", ".md": "markdown", ".sql": "sql", ".sh": "shell",
	".html": "html", ".css": "css",
}

func languageFromPath(path string) string {
	return extToLang[strings.ToLower(filepath.Ext(path))]
}

func estimateFileTokens(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return 100
	}
	// Rough: 3 chars per token
	return int(info.Size()) / 3
}
