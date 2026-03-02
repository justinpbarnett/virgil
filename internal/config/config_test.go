package config

import (
	"os"
	"path/filepath"
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

func setupTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, dir, "virgil.yaml", `
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

	writeFile(t, dir, "vocabulary.yaml", `
verbs:
  draft: draft
  remember: memory.store
types:
  blog: blog
sources:
  notes: memory
modifiers:
  today: today
`)

	writeFile(t, dir, "templates.yaml", `
templates:
  - requires: [verb]
    plan:
      - pipe: "{verb}"
`)

	pipesDir := filepath.Join(dir, "pipes")
	writeFile(t, pipesDir, "memory.yaml", `
name: memory
description: Test memory pipe
category: memory
triggers:
  exact: []
  keywords: [remember]
  patterns: []
`)

	return dir
}

func TestLoadValidConfig(t *testing.T) {
	dir := setupTestConfig(t)
	cfg, err := Load(dir)
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
	dir := setupTestConfig(t)
	cfg, err := Load(dir)
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
	dir := setupTestConfig(t)
	cfg, err := Load(dir)
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
	dir := setupTestConfig(t)
	cfg, err := Load(dir)
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
	_, err := Load("/nonexistent/path")
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
	dir := t.TempDir()

	writeFile(t, dir, "virgil.yaml", `
server:
  host: localhost
  port: 7890
database_path: ~/data/test.db
`)
	writeFile(t, dir, "vocabulary.yaml", `
verbs: {}
types: {}
sources: {}
modifiers: {}
`)
	writeFile(t, dir, "templates.yaml", `
templates: []
`)
	os.MkdirAll(filepath.Join(dir, "pipes"), 0o755)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.DatabasePath[:2] == "~/" {
		t.Errorf("expected ~ to be expanded, got %s", cfg.DatabasePath)
	}
}
