package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
    remember: memory.store
  types: {}
  sources:
    notes: memory
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
    draft: draft
  types:
    blog: blog
  sources: {}
  modifiers:
    today: today
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
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log_level debug, got %s", cfg.LogLevel)
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

	if cfg.Vocabulary.Verbs["draft"] != "draft" {
		t.Errorf("expected verb draft→draft, got %s", cfg.Vocabulary.Verbs["draft"])
	}
	if cfg.Vocabulary.Verbs["remember"] != "memory.store" {
		t.Errorf("expected verb remember→memory.store, got %s", cfg.Vocabulary.Verbs["remember"])
	}
	if cfg.Vocabulary.Sources["notes"] != "memory" {
		t.Errorf("expected source notes→memory, got %s", cfg.Vocabulary.Sources["notes"])
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
    run: alpha
  types:
    report: report
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
    build: beta
  types: {}
  sources:
    logs: beta
  modifiers:
    recent: recent
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Vocabulary.Verbs["run"] != "alpha" {
		t.Errorf("expected verb run→alpha, got %s", cfg.Vocabulary.Verbs["run"])
	}
	if cfg.Vocabulary.Verbs["build"] != "beta" {
		t.Errorf("expected verb build→beta, got %s", cfg.Vocabulary.Verbs["build"])
	}
	if cfg.Vocabulary.Types["report"] != "report" {
		t.Errorf("expected type report→report, got %s", cfg.Vocabulary.Types["report"])
	}
	if cfg.Vocabulary.Sources["logs"] != "beta" {
		t.Errorf("expected source logs→beta, got %s", cfg.Vocabulary.Sources["logs"])
	}
	if cfg.Vocabulary.Modifiers["recent"] != "recent" {
		t.Errorf("expected modifier recent→recent, got %s", cfg.Vocabulary.Modifiers["recent"])
	}
}

func TestVocabularyConflictDetection(t *testing.T) {
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
    run: alpha
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
    run: beta
  types: {}
  sources: {}
  modifiers: {}
`)

	_, err := Load(configDir, pipesDir)
	if err == nil {
		t.Fatal("expected vocabulary conflict error")
	}
	if !strings.Contains(err.Error(), "vocabulary conflict") {
		t.Errorf("expected 'vocabulary conflict' in error, got: %s", err.Error())
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
    run: shared
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
    run: shared
  types: {}
  sources: {}
  modifiers: {}
`)

	cfg, err := Load(configDir, pipesDir)
	if err != nil {
		t.Fatalf("expected no error for identical mapping, got: %v", err)
	}
	if cfg.Vocabulary.Verbs["run"] != "shared" {
		t.Errorf("expected verb run→shared, got %s", cfg.Vocabulary.Verbs["run"])
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

