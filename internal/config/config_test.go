package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating dir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func setupTestConfig(t *testing.T) (configDir, pipesDir string) {
	t.Helper()
	configDir = t.TempDir()
	pipesDir = t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 9999
provider:
  name: claude
  model: haiku
  binary: claude
log_level: debug
database_path: /tmp/test.db
`)

	// Memory pipe with vocabulary
	memDir := filepath.Join(pipesDir, "memory")
	writeFile(t, memDir, "pipe.yaml", `
name: memory
description: Test memory pipe
category: memory
triggers:
  exact: []
  keywords: [remember]
  patterns: []
vocabulary:
  verbs:
    remember: [memory.store]
  types: {}
  sources:
    notes: [memory]
  modifiers: {}
`)

	// Draft pipe with vocabulary and templates
	draftDir := filepath.Join(pipesDir, "draft")
	writeFile(t, draftDir, "pipe.yaml", `
name: draft
description: Test draft pipe
category: comms
triggers:
  exact: []
  keywords: [draft]
  patterns: []
vocabulary:
  verbs:
    draft: [draft]
  types:
    blog: [blog]
  sources: {}
  modifiers:
    today: [today]
templates:
  priority: 50
  entries:
    - requires: [verb]
      plan:
        - pipe: "{verb}"
`)

	return configDir, pipesDir
}

func TestLoadValidConfig(t *testing.T) {
	configDir, pipesDir := setupTestConfig(t)
	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 9999 {
		t.Errorf("expected port 9999, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("expected host localhost, got %s", cfg.Server.Host)
	}
	if cfg.Provider.Model != "haiku" {
		t.Errorf("expected model haiku, got %s", cfg.Provider.Model)
	}
	if cfg.LogLevel != Debug {
		t.Errorf("expected log_level debug, got %v", cfg.LogLevel)
	}
	if cfg.DatabasePath != "/tmp/test.db" {
		t.Errorf("expected database_path /tmp/test.db, got %s", cfg.DatabasePath)
	}
}

func TestLoadVocabulary(t *testing.T) {
	configDir, pipesDir := setupTestConfig(t)
	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Vocabulary.Verbs["draft"][0] != "draft" {
		t.Errorf("expected verb draft→draft, got %s", cfg.Vocabulary.Verbs["draft"][0])
	}
	if cfg.Vocabulary.Verbs["remember"][0] != "memory.store" {
		t.Errorf("expected verb remember→memory.store, got %s", cfg.Vocabulary.Verbs["remember"][0])
	}
	if cfg.Vocabulary.Sources["notes"][0] != "memory" {
		t.Errorf("expected source notes→memory, got %s", cfg.Vocabulary.Sources["notes"][0])
	}
}

func TestLoadTemplates(t *testing.T) {
	configDir, pipesDir := setupTestConfig(t)
	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.Templates.Templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(cfg.Templates.Templates))
	}
	tmpl := cfg.Templates.Templates[0]
	if len(tmpl.Requires) != 1 || tmpl.Requires[0] != "verb" {
		t.Errorf("expected requires [verb], got %v", tmpl.Requires)
	}
}

func TestLoadPipeConfigs(t *testing.T) {
	configDir, pipesDir := setupTestConfig(t)
	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	memCfg, ok := cfg.Pipes["memory"]
	if !ok {
		t.Fatal("expected memory pipe config")
	}
	if memCfg.Category != "memory" {
		t.Errorf("expected category memory, got %s", memCfg.Category)
	}
}

func TestLoadMissingConfigDir(t *testing.T) {
	_, err := Load("/nonexistent/path", "/nonexistent/pipes")
	if err == nil {
		t.Error("expected error for missing config directory")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	result := expandHome("~/test/path")
	expected := filepath.Join(home, "test/path")
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Non-home path should pass through
	result = expandHome("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %s", result)
	}
}

func TestLoadDatabasePathExpansion(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
database_path: ~/data/test.db
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.DatabasePath[:2] == "~/" {
		t.Errorf("expected ~ to be expanded, got %s", cfg.DatabasePath)
	}
}

func TestVocabularyMergeFromMultiplePipes(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	writeFile(t, filepath.Join(pipesDir, "alpha"), "pipe.yaml", `
name: alpha
description: Alpha pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    run: [alpha]
  types:
    report: [report]
  sources: {}
  modifiers: {}
`)

	writeFile(t, filepath.Join(pipesDir, "beta"), "pipe.yaml", `
name: beta
description: Beta pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    build: [beta]
  types: {}
  sources:
    logs: [beta]
  modifiers:
    recent: [recent]
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Vocabulary.Verbs["run"][0] != "alpha" {
		t.Errorf("expected verb run→alpha, got %s", cfg.Vocabulary.Verbs["run"][0])
	}
	if cfg.Vocabulary.Verbs["build"][0] != "beta" {
		t.Errorf("expected verb build→beta, got %s", cfg.Vocabulary.Verbs["build"][0])
	}
	if cfg.Vocabulary.Types["report"][0] != "report" {
		t.Errorf("expected type report→report, got %s", cfg.Vocabulary.Types["report"][0])
	}
	if cfg.Vocabulary.Sources["logs"][0] != "beta" {
		t.Errorf("expected source logs→beta, got %s", cfg.Vocabulary.Sources["logs"][0])
	}
	if cfg.Vocabulary.Modifiers["recent"][0] != "recent" {
		t.Errorf("expected modifier recent→recent, got %s", cfg.Vocabulary.Modifiers["recent"][0])
	}
}

func TestVocabularyMultipleMappings(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	writeFile(t, filepath.Join(pipesDir, "alpha"), "pipe.yaml", `
name: alpha
description: Alpha pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    run: [alpha]
  types: {}
  sources: {}
  modifiers: {}
`)

	writeFile(t, filepath.Join(pipesDir, "beta"), "pipe.yaml", `
name: beta
description: Beta pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    run: [beta]
  types: {}
  sources: {}
  modifiers: {}
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both mappings should be preserved
	if len(cfg.Vocabulary.Verbs["run"]) != 2 {
		t.Errorf("expected 2 mappings for 'run', got %d: %v", len(cfg.Vocabulary.Verbs["run"]), cfg.Vocabulary.Verbs["run"])
	}
}

func TestVocabularyIdenticalMappingOK(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	writeFile(t, filepath.Join(pipesDir, "alpha"), "pipe.yaml", `
name: alpha
description: Alpha pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    run: [shared]
  types: {}
  sources: {}
  modifiers: {}
`)

	writeFile(t, filepath.Join(pipesDir, "beta"), "pipe.yaml", `
name: beta
description: Beta pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
vocabulary:
  verbs:
    run: [shared]
  types: {}
  sources: {}
  modifiers: {}
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("expected no error for identical mapping, got: %v", err)
	}
	if len(cfg.Vocabulary.Verbs["run"]) != 2 || cfg.Vocabulary.Verbs["run"][0] != "shared" || cfg.Vocabulary.Verbs["run"][1] != "shared" {
		t.Errorf("expected verb run to have [shared, shared], got %v", cfg.Vocabulary.Verbs["run"])
	}
}

func TestTemplatePriorityOrdering(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	// Low priority pipe (should come first)
	writeFile(t, filepath.Join(pipesDir, "alpha"), "pipe.yaml", `
name: alpha
description: Alpha pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
templates:
  priority: 10
  entries:
    - requires: [verb, type]
      plan:
        - pipe: alpha
`)

	// High priority pipe (should come last)
	writeFile(t, filepath.Join(pipesDir, "beta"), "pipe.yaml", `
name: beta
description: Beta pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
templates:
  priority: 90
  entries:
    - requires: [verb]
      plan:
        - pipe: beta
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.Templates.Templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(cfg.Templates.Templates))
	}

	// First template should be from alpha (priority 10)
	if len(cfg.Templates.Templates[0].Plan) != 1 || cfg.Templates.Templates[0].Plan[0].Pipe != "alpha" {
		t.Errorf("expected first template from alpha, got pipe=%s", cfg.Templates.Templates[0].Plan[0].Pipe)
	}

	// Second template should be from beta (priority 90)
	if len(cfg.Templates.Templates[1].Plan) != 1 || cfg.Templates.Templates[1].Plan[0].Pipe != "beta" {
		t.Errorf("expected second template from beta, got pipe=%s", cfg.Templates.Templates[1].Plan[0].Pipe)
	}
}

func TestTemplateSpecificityOrdering(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	// Same priority, different specificity
	writeFile(t, filepath.Join(pipesDir, "alpha"), "pipe.yaml", `
name: alpha
description: Alpha pipe
category: test
triggers:
  exact: []
  keywords: []
  patterns: []
templates:
  priority: 50
  entries:
    - requires: [verb]
      plan:
        - pipe: less-specific
    - requires: [verb, type, source]
      plan:
        - pipe: most-specific
    - requires: [verb, type]
      plan:
        - pipe: mid-specific
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.Templates.Templates) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(cfg.Templates.Templates))
	}

	// Should be ordered: most-specific (3 requires), mid-specific (2), less-specific (1)
	if cfg.Templates.Templates[0].Plan[0].Pipe != "most-specific" {
		t.Errorf("expected first template most-specific, got %s", cfg.Templates.Templates[0].Plan[0].Pipe)
	}
	if cfg.Templates.Templates[1].Plan[0].Pipe != "mid-specific" {
		t.Errorf("expected second template mid-specific, got %s", cfg.Templates.Templates[1].Plan[0].Pipe)
	}
	if cfg.Templates.Templates[2].Plan[0].Pipe != "less-specific" {
		t.Errorf("expected third template less-specific, got %s", cfg.Templates.Templates[2].Plan[0].Pipe)
	}
}

func TestPipeWithNoVocabularyOrTemplates(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	writeFile(t, filepath.Join(pipesDir, "chat"), "pipe.yaml", `
name: chat
description: Chat pipe
category: general
triggers:
  exact: []
  keywords: [chat]
  patterns: []
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.Vocabulary.Verbs) != 0 {
		t.Errorf("expected no verbs, got %d", len(cfg.Vocabulary.Verbs))
	}
	if len(cfg.Templates.Templates) != 0 {
		t.Errorf("expected no templates, got %d", len(cfg.Templates.Templates))
	}

	if _, ok := cfg.Pipes["chat"]; !ok {
		t.Error("expected chat pipe to be loaded")
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		input string
		want  LogLevel
	}{
		{"silent", Silent},
		{"error", Error},
		{"warn", Warn},
		{"info", Info},
		{"debug", Debug},
		{"verbose", Verbose},
		{"SILENT", Silent},
		{"Info", Info},
		{"DEBUG", Debug},
		{"", Info},
		{"unknown", Info},
		{"trace", Info},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseLogLevel(tc.input)
			if got != tc.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestToSlogLevel(t *testing.T) {
	// Silent should be above error so nothing prints
	if ToSlogLevel(Silent) <= slog.LevelError {
		t.Error("Silent slog level should be above error")
	}
	if ToSlogLevel(Error) != slog.LevelError {
		t.Errorf("Error → expected slog.LevelError, got %v", ToSlogLevel(Error))
	}
	if ToSlogLevel(Warn) != slog.LevelWarn {
		t.Errorf("Warn → expected slog.LevelWarn, got %v", ToSlogLevel(Warn))
	}
	if ToSlogLevel(Info) != slog.LevelInfo {
		t.Errorf("Info → expected slog.LevelInfo, got %v", ToSlogLevel(Info))
	}
	if ToSlogLevel(Debug) != slog.LevelDebug {
		t.Errorf("Debug → expected slog.LevelDebug, got %v", ToSlogLevel(Debug))
	}
	if ToSlogLevel(Verbose) != slog.LevelDebug {
		t.Errorf("Verbose → expected slog.LevelDebug, got %v", ToSlogLevel(Verbose))
	}
}

func TestEffectiveLogLevel(t *testing.T) {
	// Pipe level set → use pipe level
	pc := PipeConfig{PipeLogLevel: Debug}
	if got := pc.EffectiveLogLevel(Info); got != Debug {
		t.Errorf("expected pipe level Debug, got %v", got)
	}

	// Pipe level unset → use global default
	pc = PipeConfig{PipeLogLevel: Unset}
	if got := pc.EffectiveLogLevel(Warn); got != Warn {
		t.Errorf("expected global default Warn, got %v", got)
	}
}

func TestLogLevelString(t *testing.T) {
	cases := []struct {
		level LogLevel
		want  string
	}{
		{Unset, ""},
		{Silent, "silent"},
		{Error, "error"},
		{Warn, "warn"},
		{Info, "info"},
		{Debug, "debug"},
		{Verbose, "verbose"},
	}

	for _, tc := range cases {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("LogLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestLogLevelUnmarshalYAML(t *testing.T) {
	type wrapper struct {
		Level LogLevel `yaml:"level"`
	}

	cases := []struct {
		name string
		yaml string
		want LogLevel
	}{
		{"debug", "level: debug", Debug},
		{"info", "level: info", Info},
		{"silent", "level: silent", Silent},
		{"verbose", "level: verbose", Verbose},
		{"empty string", "level: \"\"", Unset},
		{"absent field", "{}", Unset},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var w wrapper
			if err := yaml.Unmarshal([]byte(tc.yaml), &w); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if w.Level != tc.want {
				t.Errorf("got %v, want %v", w.Level, tc.want)
			}
		})
	}
}

func TestToSlogLevelUnset(t *testing.T) {
	if ToSlogLevel(Unset) != slog.LevelInfo {
		t.Errorf("Unset → expected slog.LevelInfo, got %v", ToSlogLevel(Unset))
	}
}

func TestEffectiveModel(t *testing.T) {
	// Pipe model set → use pipe model
	pc := PipeConfig{Model: "haiku"}
	if got := pc.EffectiveModel("sonnet"); got != "haiku" {
		t.Errorf("expected pipe model haiku, got %s", got)
	}

	// Pipe model unset → use global default
	pc = PipeConfig{}
	if got := pc.EffectiveModel("sonnet"); got != "sonnet" {
		t.Errorf("expected global default sonnet, got %s", got)
	}
}

func TestEffectiveMaxTurns(t *testing.T) {
	// MaxTurns set to 0 → returns 0
	zero := 0
	pc := PipeConfig{MaxTurns: &zero}
	if got := pc.EffectiveMaxTurns(); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}

	// MaxTurns set to 3 → returns 3
	three := 3
	pc = PipeConfig{MaxTurns: &three}
	if got := pc.EffectiveMaxTurns(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}

	// MaxTurns nil → returns default 1
	pc = PipeConfig{}
	if got := pc.EffectiveMaxTurns(); got != 1 {
		t.Errorf("expected default 1, got %d", got)
	}
}

func TestLoadPipeFormatTemplates(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	writeFile(t, filepath.Join(pipesDir, "calendar"), "pipe.yaml", `
name: calendar
description: Calendar pipe
category: time
triggers:
  exact: []
  keywords: [calendar]
  patterns: []
format:
  list: |
    {{.Count}} events today
  structured: |
    Event: {{.title}}
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	calCfg, ok := cfg.Pipes["calendar"]
	if !ok {
		t.Fatal("expected calendar pipe config")
	}
	if len(calCfg.Format) != 2 {
		t.Fatalf("expected 2 format entries, got %d", len(calCfg.Format))
	}
	if !strings.Contains(calCfg.Format["list"], "Count") {
		t.Errorf("expected list format to contain 'Count', got: %s", calCfg.Format["list"])
	}
	if !strings.Contains(calCfg.Format["structured"], "title") {
		t.Errorf("expected structured format to contain 'title', got: %s", calCfg.Format["structured"])
	}
}

func TestMemoryConfigParsing(t *testing.T) {
	var pc PipeConfig
	data := []byte(`
name: educate
memory:
  context:
    - type: topic_history
      depth: 30d
    - type: user_preferences
  budget: 2000
`)
	if err := UnmarshalPipeConfig(data, &pc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(pc.Memory.Context) != 2 {
		t.Fatalf("expected 2 context entries, got %d", len(pc.Memory.Context))
	}
	if pc.Memory.Context[0].Type != "topic_history" {
		t.Errorf("expected type=topic_history, got %s", pc.Memory.Context[0].Type)
	}
	if pc.Memory.Context[0].Depth != "30d" {
		t.Errorf("expected depth=30d, got %s", pc.Memory.Context[0].Depth)
	}
	if pc.Memory.Context[1].Type != "user_preferences" {
		t.Errorf("expected type=user_preferences, got %s", pc.Memory.Context[1].Type)
	}
	if pc.Memory.Budget != 2000 {
		t.Errorf("expected budget=2000, got %d", pc.Memory.Budget)
	}
	if pc.Memory.Disabled {
		t.Error("expected disabled=false")
	}
}

func TestMemoryConfigDisabled(t *testing.T) {
	var pc PipeConfig
	data := []byte(`
name: calendar
memory:
  disabled: true
`)
	if err := UnmarshalPipeConfig(data, &pc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !pc.Memory.Disabled {
		t.Error("expected disabled=true")
	}
}

func TestDefaultMemoryConfig(t *testing.T) {
	cfg := DefaultMemoryConfig()
	if len(cfg.Context) != 1 {
		t.Fatalf("expected 1 context entry, got %d", len(cfg.Context))
	}
	if cfg.Context[0].Type != "working_state" {
		t.Errorf("expected type=working_state, got %s", cfg.Context[0].Type)
	}
	if cfg.Budget != 500 {
		t.Errorf("expected budget=500, got %d", cfg.Budget)
	}
	if cfg.Disabled {
		t.Error("expected disabled=false")
	}
}

func TestMemoryConfigAbsentGivesZeroValue(t *testing.T) {
	var pc PipeConfig
	data := []byte(`name: chat`)
	if err := UnmarshalPipeConfig(data, &pc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if pc.Memory.Disabled {
		t.Error("expected disabled=false when absent")
	}
	if len(pc.Memory.Context) != 0 {
		t.Errorf("expected no context entries when absent, got %d", len(pc.Memory.Context))
	}
}

func TestEffectiveProvider(t *testing.T) {
	// Pipe provider set → use pipe provider
	pc := PipeConfig{Provider: "openai"}
	if got := pc.EffectiveProvider("anthropic"); got != "openai" {
		t.Errorf("expected pipe provider openai, got %s", got)
	}

	// Pipe provider unset → use global default
	pc = PipeConfig{}
	if got := pc.EffectiveProvider("anthropic"); got != "anthropic" {
		t.Errorf("expected global default anthropic, got %s", got)
	}
}

func TestEffectiveMaxTokens(t *testing.T) {
	// Pipe max_tokens set → use pipe value
	val := 4096
	pc := PipeConfig{MaxTokens: &val}
	if got := pc.EffectiveMaxTokens(8192); got != 4096 {
		t.Errorf("expected pipe max_tokens 4096, got %d", got)
	}

	// Pipe max_tokens unset → use global default
	pc = PipeConfig{}
	if got := pc.EffectiveMaxTokens(8192); got != 8192 {
		t.Errorf("expected global default 8192, got %d", got)
	}

	// Zero value pointer → use override value
	zero := 0
	pc = PipeConfig{MaxTokens: &zero}
	if got := pc.EffectiveMaxTokens(8192); got != 0 {
		t.Errorf("expected 0 (explicit pipe override), got %d", got)
	}
}

func TestProviderConfigMaxTokensDefault(t *testing.T) {
	configDir, pipesDir := setupTestConfig(t)
	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	// Default max_tokens should be 8192 when not set in yaml
	if cfg.Provider.MaxTokens != 8192 {
		t.Errorf("expected default MaxTokens 8192, got %d", cfg.Provider.MaxTokens)
	}
}

func TestPipeConfigModelAndMaxTurnsFromYAML(t *testing.T) {
	var pc PipeConfig
	data := []byte(`
name: test
model: haiku
max_turns: 0
`)
	if err := UnmarshalPipeConfig(data, &pc); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if pc.Model != "haiku" {
		t.Errorf("expected model haiku, got %s", pc.Model)
	}
	if pc.MaxTurns == nil {
		t.Fatal("expected MaxTurns to be set")
	}
	if *pc.MaxTurns != 0 {
		t.Errorf("expected MaxTurns 0, got %d", *pc.MaxTurns)
	}
}

func TestAckConfigDefaults(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Ack.Provider != "gemini" {
		t.Errorf("expected ack provider gemini, got %s", cfg.Ack.Provider)
	}
	if cfg.Ack.Model != "gemini-3-flash-preview" {
		t.Errorf("expected ack model gemini-3-flash-preview, got %s", cfg.Ack.Model)
	}
	if cfg.Ack.MaxTokens != 256 {
		t.Errorf("expected ack max_tokens 256, got %d", cfg.Ack.MaxTokens)
	}
}

func TestAckConfigFromYAML(t *testing.T) {
	configDir := t.TempDir()
	pipesDir := t.TempDir()

	writeFile(t, configDir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
ack:
  provider: openai
  model: gpt-4o-mini
  max_tokens: 128
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Ack.Provider != "openai" {
		t.Errorf("expected ack provider openai, got %s", cfg.Ack.Provider)
	}
	if cfg.Ack.Model != "gpt-4o-mini" {
		t.Errorf("expected ack model gpt-4o-mini, got %s", cfg.Ack.Model)
	}
	if cfg.Ack.MaxTokens != 128 {
		t.Errorf("expected ack max_tokens 128, got %d", cfg.Ack.MaxTokens)
	}
}

func TestDailyPath(t *testing.T) {
	dir := t.TempDir()
	got := DailyPath(dir, "server", ".log")
	date := time.Now().Format("2006-01-02")
	want := filepath.Join(dir, "server-"+date+".log")
	if got != want {
		t.Errorf("DailyPath = %q, want %q", got, want)
	}
}
