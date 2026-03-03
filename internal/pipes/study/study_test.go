package study

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

func TestCountTokens(t *testing.T) {
	tests := []struct {
		input string
		min   int
		max   int
	}{
		{"", 0, 0},
		{"hello", 1, 5},
		{"func main() { fmt.Println(\"hello\") }", 5, 20},
		{"a", 1, 1},
	}
	for _, tt := range tests {
		got := CountTokens(tt.input)
		if got < tt.min || got > tt.max {
			t.Errorf("CountTokens(%q) = %d, want between %d and %d", tt.input, got, tt.min, tt.max)
		}
	}
}

func TestFitsInBudget(t *testing.T) {
	if !FitsInBudget(0, 100, 100) {
		t.Error("expected 0+100 to fit in 100")
	}
	if FitsInBudget(50, 51, 100) {
		t.Error("expected 50+51 to not fit in 100")
	}
	if !FitsInBudget(99, 1, 100) {
		t.Error("expected 99+1 to fit in 100")
	}
}

func TestMaxCompressionTier(t *testing.T) {
	tests := []struct {
		flag string
		want int
	}{
		{"none", TierNone},
		{"structural", TierElision},
		{"ai", TierAI},
		{"", TierAI},
		{"unknown", TierAI},
	}
	for _, tt := range tests {
		got := maxCompressionTier(tt.flag)
		if got != tt.want {
			t.Errorf("maxCompressionTier(%q) = %d, want %d", tt.flag, got, tt.want)
		}
	}
}

func TestRankItems(t *testing.T) {
	items := []ExtractedItem{
		{
			ID:          "a.go:10",
			Content:     "func HandleAuth(ctx context.Context) error {",
			Granularity: GranDeclaration,
			TokenCount:  50,
			Metadata:    ItemMetadata{Path: "a.go", Kind: KindFunction, Distance: 0},
		},
		{
			ID:          "b.go:5",
			Content:     "type Config struct { Name string }",
			Granularity: GranDeclaration,
			TokenCount:  30,
			Metadata:    ItemMetadata{Path: "b.go", Kind: KindType, Distance: 2},
		},
		{
			ID:          "c.go:1",
			Content:     "// Package auth handles authentication",
			Granularity: GranFileSummary,
			TokenCount:  20,
			Metadata:    ItemMetadata{Path: "c.go", Kind: KindFile, Distance: 1},
		},
	}

	ranked := rankItems(items, "auth", "builder", map[string]int{"a.go:10": 2})

	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked items, got %d", len(ranked))
	}

	// The auth function at distance 0 should rank highest for a builder
	if ranked[0].ID != "a.go:10" {
		t.Errorf("expected a.go:10 to rank first, got %s", ranked[0].ID)
	}

	// All scores should be positive
	for _, r := range ranked {
		if r.Score <= 0 {
			t.Errorf("expected positive score for %s, got %f", r.ID, r.Score)
		}
	}

	// Scores should be in descending order
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Score > ranked[i-1].Score {
			t.Errorf("scores not in descending order at index %d", i)
		}
	}
}

func TestGreedyFill(t *testing.T) {
	ranked := []RankedItem{
		{ExtractedItem: ExtractedItem{ID: "a", TokenCount: 100}, Score: 0.9},
		{ExtractedItem: ExtractedItem{ID: "b", TokenCount: 50}, Score: 0.8},
		{ExtractedItem: ExtractedItem{ID: "c", TokenCount: 80}, Score: 0.7},
		{ExtractedItem: ExtractedItem{ID: "d", TokenCount: 30}, Score: 0.6},
	}

	included, excluded, used := greedyFill(ranked, 200)

	if len(included) != 3 {
		t.Errorf("expected 3 included, got %d", len(included))
	}
	if len(excluded) != 1 {
		t.Errorf("expected 1 excluded, got %d", len(excluded))
	}
	if used != 180 { // 100 + 50 + 30
		t.Errorf("expected used=180, got %d", used)
	}
}

func TestExtractSignature(t *testing.T) {
	decl := `// HandleAuth processes authentication requests.
func HandleAuth(ctx context.Context, creds Credentials) (*Session, error) {
	if creds.Username == "" {
		return nil, ErrMissingUsername
	}
	session, err := authenticate(ctx, creds)
	if err != nil {
		return nil, err
	}
	return session, nil
}`

	sig := extractSignature(decl)

	// Should contain the comment and function signature
	if !contains(sig, "HandleAuth") {
		t.Error("signature should contain function name")
	}
	if !contains(sig, "context.Context") {
		t.Error("signature should contain parameter types")
	}
	// Should be shorter than the original
	if len(sig) >= len(decl) {
		t.Error("signature should be shorter than full declaration")
	}
}

func TestTruncateBlock(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line content"
	}
	content := ""
	for i, l := range lines {
		if i > 0 {
			content += "\n"
		}
		content += l
	}

	truncated := truncateBlock(content, 3)

	if !contains(truncated, "omitted") {
		t.Error("truncated block should contain omission marker")
	}
	if CountTokens(truncated) >= CountTokens(content) {
		t.Error("truncated should be shorter than original")
	}
}

func TestCollapseImports(t *testing.T) {
	content := `package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {}`

	collapsed := collapseImports(content)

	if contains(collapsed, "import (") {
		t.Error("should not contain 'import (' after collapsing")
	}
	if !contains(collapsed, "imports:") {
		t.Error("should contain 'imports:' summary")
	}
	if !contains(collapsed, "func main()") {
		t.Error("should preserve non-import content")
	}
}

func TestDeduplicateCandidates(t *testing.T) {
	candidates := []Candidate{
		{ID: "a.go:10", Source: "text"},
		{ID: "b.go:5", Source: "index"},
		{ID: "a.go:10", Source: "lsp"}, // duplicate
		{ID: "c.go:1", Source: "text"},
	}

	deduped := deduplicateCandidates(candidates)

	if len(deduped) != 3 {
		t.Errorf("expected 3 after dedup, got %d", len(deduped))
	}
}

func TestShouldSkipDir(t *testing.T) {
	skips := []string{".git", "node_modules", "vendor", "__pycache__"}
	for _, dir := range skips {
		if !shouldSkipDir(dir) {
			t.Errorf("expected to skip %s", dir)
		}
	}
	if shouldSkipDir("internal") {
		t.Error("should not skip 'internal'")
	}
}

func TestIsSearchableFile(t *testing.T) {
	searchable := []string{"main.go", "app.py", "index.ts", "config.yaml"}
	for _, f := range searchable {
		if !isSearchableFile(f) {
			t.Errorf("expected %s to be searchable", f)
		}
	}
	notSearchable := []string{"image.png", "binary.exe", "data.bin"}
	for _, f := range notSearchable {
		if isSearchableFile(f) {
			t.Errorf("expected %s to not be searchable", f)
		}
	}
}

func TestLanguageFromPath(t *testing.T) {
	tests := map[string]string{
		"main.go":     "go",
		"app.py":      "python",
		"index.ts":    "typescript",
		"config.yaml": "yaml",
		"unknown.xyz": "",
	}
	for path, want := range tests {
		got := languageFromPath(path)
		if got != want {
			t.Errorf("languageFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create test files
	writeFile(t, dir, "main.go", `package main

import "fmt"

// HandleAuth processes authentication requests.
func HandleAuth(name string) {
	fmt.Println("Hello", name)
}

func helper() {
	fmt.Println("helper")
}
`)

	writeFile(t, dir, "config.yaml", `name: test
version: 1
settings:
  debug: true
`)

	writeFile(t, dir, "internal/auth/service.go", `package auth

// AuthService handles authentication.
type AuthService interface {
	Authenticate(username, password string) error
	Validate(token string) bool
}
`)

	writeFile(t, dir, "internal/auth/handler.go", `package auth

import "fmt"

func HandleLogin(username string) error {
	fmt.Println("login", username)
	return nil
}
`)

	return dir
}

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	path := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodebaseBackendDiscover(t *testing.T) {
	dir := testDir(t)
	backend := newCodebaseBackend(dir, nil)

	candidates, err := backend.Discover(context.Background(), "auth", "", "normal")
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) == 0 {
		t.Fatal("expected candidates for 'auth' query")
	}

	// Should find auth-related files
	found := false
	for _, c := range candidates {
		if contains(c.Location.Path, "auth") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find auth-related candidates")
	}
}

func TestCodebaseBackendExtract(t *testing.T) {
	dir := testDir(t)
	backend := newCodebaseBackend(dir, nil)

	candidates := []Candidate{
		{ID: "main.go", Source: "index", Location: Location{Path: "main.go"}},
	}

	items, err := backend.Extract(context.Background(), candidates, "builder")
	if err != nil {
		t.Fatal(err)
	}

	if len(items) == 0 {
		t.Fatal("expected extracted items")
	}

	if items[0].Content == "" {
		t.Error("expected non-empty content")
	}
	if items[0].TokenCount == 0 {
		t.Error("expected non-zero token count")
	}
	if items[0].Metadata.Language != "go" {
		t.Errorf("expected language=go, got %s", items[0].Metadata.Language)
	}
}

func TestFileBackendDiscover(t *testing.T) {
	dir := testDir(t)
	backend := newFileBackend(dir, nil)

	candidates, err := backend.Discover(context.Background(), "config", "", "normal")
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) == 0 {
		t.Fatal("expected candidates for 'config' query")
	}

	found := false
	for _, c := range candidates {
		if contains(c.Location.Path, "config") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find config-related files")
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMemoryBackendDiscover(t *testing.T) {
	s := testStore(t)
	if err := s.Save("authentication flow uses OAuth2 tokens", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("database uses SQLite with FTS5 indexing", nil); err != nil {
		t.Fatal(err)
	}

	backend := newMemoryBackend(s, nil)

	candidates, err := backend.Discover(context.Background(), "authentication", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) == 0 {
		t.Fatal("expected candidates for 'authentication' query")
	}
}

func TestHandlerCodebaseSource(t *testing.T) {
	dir := testDir(t)

	handler := NewHandler(Config{
		WorkDir: dir,
		PipeConfig: config.PipeConfig{
			Prompts: config.PromptsConfig{
				System: "You are a summarizer.",
			},
		},
	})

	input := envelope.New("test", "study")
	input.Content = "auth"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{
		"source": "codebase",
		"role":   "builder",
		"budget": "8000",
		"depth":  "normal",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if result.Pipe != "study" {
		t.Errorf("expected pipe=study, got %s", result.Pipe)
	}
	if result.Action != "gather" {
		t.Errorf("expected action=gather, got %s", result.Action)
	}
	if result.ContentType != envelope.ContentStructured {
		t.Errorf("expected structured content, got %s", result.ContentType)
	}
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}

	output, ok := result.Content.(*StudyOutput)
	if !ok {
		t.Fatalf("expected *StudyOutput content, got %T", result.Content)
	}
	if output.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(output.Items) == 0 {
		t.Error("expected items in output")
	}
	if output.Metadata.Source != "codebase" {
		t.Errorf("expected source=codebase, got %s", output.Metadata.Source)
	}
}

func TestHandlerMemorySource(t *testing.T) {
	s := testStore(t)
	if err := s.Save("the auth system uses JWT tokens for session management", nil); err != nil {
		t.Fatal(err)
	}

	handler := NewHandler(Config{
		Store: s,
		PipeConfig: config.PipeConfig{
			Prompts: config.PromptsConfig{
				System: "You are a summarizer.",
			},
		},
	})

	input := envelope.New("test", "study")
	input.Content = "auth"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{
		"source": "memory",
		"budget": "4000",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}

	output, ok := result.Content.(*StudyOutput)
	if !ok {
		t.Fatalf("expected *StudyOutput content, got %T", result.Content)
	}
	if len(output.Items) == 0 {
		t.Error("expected items from memory")
	}
}

func TestHandlerNoQueryOrEntry(t *testing.T) {
	handler := NewHandler(Config{
		WorkDir: t.TempDir(),
	})

	input := envelope.New("test", "study")
	result := handler(input, map[string]string{"source": "codebase"})

	if result.Error == nil {
		t.Fatal("expected error for missing query/entry")
	}
	if result.Error.Severity != envelope.SeverityFatal {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
}

func TestHandlerUnsupportedSource(t *testing.T) {
	handler := NewHandler(Config{
		WorkDir: t.TempDir(),
	})

	input := envelope.New("test", "study")
	input.Content = "test query"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{"source": "web"})

	if result.Error == nil {
		t.Fatal("expected error for unsupported source")
	}
}

func TestHandlerBudgetConstraint(t *testing.T) {
	dir := testDir(t)

	handler := NewHandler(Config{
		WorkDir: dir,
	})

	input := envelope.New("test", "study")
	input.Content = "auth"
	input.ContentType = envelope.ContentText

	// Very small budget
	result := handler(input, map[string]string{
		"source": "codebase",
		"budget": "50",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}

	output, ok := result.Content.(*StudyOutput)
	if !ok {
		t.Fatalf("expected *StudyOutput, got %T", result.Content)
	}

	// Budget should be respected
	if output.Metadata.Coverage.BudgetUsed > 50 {
		t.Errorf("budget exceeded: used %d, limit 50", output.Metadata.Coverage.BudgetUsed)
	}
}

func TestHandlerCompressionNone(t *testing.T) {
	dir := testDir(t)

	handler := NewHandler(Config{
		WorkDir: dir,
	})

	input := envelope.New("test", "study")
	input.Content = "auth"
	input.ContentType = envelope.ContentText

	result := handler(input, map[string]string{
		"source":      "codebase",
		"budget":      "500",
		"compression": "none",
	})

	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}

	output := result.Content.(*StudyOutput)
	if output.Metadata.Coverage.CompressionTier > TierNone {
		t.Errorf("expected tier 0 with compression=none, got tier %d", output.Metadata.Coverage.CompressionTier)
	}
}

func TestRoleAlignmentScoring(t *testing.T) {
	funcItem := ExtractedItem{
		Granularity: GranDeclaration,
		Metadata:    ItemMetadata{Kind: KindFunction},
	}
	ifaceItem := ExtractedItem{
		Granularity: GranInterface,
		Metadata:    ItemMetadata{Kind: KindInterface},
	}

	// Builder should prefer functions
	builderFunc := roleAlignmentScore(funcItem, "builder")
	builderIface := roleAlignmentScore(ifaceItem, "builder")
	if builderFunc <= builderIface {
		t.Error("builder should prefer function declarations over interfaces")
	}

	// Reviewer should prefer interfaces
	reviewerFunc := roleAlignmentScore(funcItem, "reviewer")
	reviewerIface := roleAlignmentScore(ifaceItem, "reviewer")
	if reviewerIface <= reviewerFunc {
		t.Error("reviewer should prefer interfaces over function declarations")
	}

	// Planner should prefer interfaces/summaries
	plannerIface := roleAlignmentScore(ifaceItem, "planner")
	plannerFunc := roleAlignmentScore(funcItem, "planner")
	if plannerIface <= plannerFunc {
		t.Error("planner should prefer interfaces over functions")
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Now()

	recent := recencyScore(now.Add(-1*time.Hour), now)
	weekOld := recencyScore(now.Add(-5*24*time.Hour), now)
	monthOld := recencyScore(now.Add(-20*24*time.Hour), now)

	if recent <= weekOld {
		t.Error("recent files should score higher than week-old files")
	}
	if weekOld <= monthOld {
		t.Error("week-old files should score higher than month-old files")
	}
}

func TestCompress(t *testing.T) {
	ranked := []RankedItem{
		{ExtractedItem: ExtractedItem{ID: "a", Content: "func A() {}", TokenCount: 100, Granularity: GranDeclaration}, Score: 0.9},
		{ExtractedItem: ExtractedItem{ID: "b", Content: "func B() {}", TokenCount: 80, Granularity: GranDeclaration}, Score: 0.8},
		{ExtractedItem: ExtractedItem{ID: "c", Content: "func C() {}", TokenCount: 60, Granularity: GranBlock}, Score: 0.7},
		{ExtractedItem: ExtractedItem{ID: "d", Content: "func D() {}", TokenCount: 40, Granularity: GranBlock}, Score: 0.6},
	}

	result := compress(context.Background(), ranked, 200, TierNone, nil, "", nil)

	if result.Coverage.BudgetUsed > 200 {
		t.Errorf("budget exceeded: used %d, limit 200", result.Coverage.BudgetUsed)
	}
	if result.Coverage.IncludedItems == 0 {
		t.Error("expected at least one included item")
	}
	if result.Coverage.BudgetTotal != 200 {
		t.Errorf("expected budget_total=200, got %d", result.Coverage.BudgetTotal)
	}
}

func TestCompressWithAI(t *testing.T) {
	provider := &testutil.MockProvider{Response: "Dense summary of excluded items."}

	ranked := []RankedItem{
		{ExtractedItem: ExtractedItem{ID: "a", Content: "func A() { /* lots of code */ }", TokenCount: 150, Metadata: ItemMetadata{Path: "pkg/a.go"}}, Score: 0.9},
		{ExtractedItem: ExtractedItem{ID: "b", Content: "func B() { /* more code */ }", TokenCount: 120, Metadata: ItemMetadata{Path: "pkg/b.go"}}, Score: 0.8},
		{ExtractedItem: ExtractedItem{ID: "c", Content: "func C() { /* even more */ }", TokenCount: 100, Metadata: ItemMetadata{Path: "pkg/c.go"}}, Score: 0.7},
	}

	// Budget only fits the first item
	result := compress(context.Background(), ranked, 250, TierAI, provider, "You are a summarizer.", nil)

	if result.Coverage.CompressionTier < TierDemotion {
		t.Error("expected compression tier >= 1 when budget is tight")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
