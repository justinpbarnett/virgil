package pipehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePipeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	path := filepath.Join(dir, "pipe.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing pipe.yaml: %v", err)
	}
	return path
}

func TestLoadPipeConfigFrom_InjectsIdentity(t *testing.T) {
	path := writePipeYAML(t, t.TempDir(), `
name: test
description: test pipe
prompts:
  system: |
    You are a professional writer.
  templates:
    formal: |
      Write in a formal tone.
    casual: |
      Write in a casual tone.
`)

	t.Setenv(EnvIdentity, "I am Virgil, your guide.")

	pc, err := LoadPipeConfigFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(pc.Prompts.System, "I am Virgil, your guide.") {
		t.Errorf("expected system prompt to start with identity, got: %s", pc.Prompts.System)
	}
	if !strings.Contains(pc.Prompts.System, "You are a professional writer.") {
		t.Errorf("expected system prompt to contain original prompt, got: %s", pc.Prompts.System)
	}

	for name, tmpl := range pc.Prompts.Templates {
		if !strings.HasPrefix(tmpl, "I am Virgil, your guide.") {
			t.Errorf("template %q should start with identity, got: %s", name, tmpl)
		}
	}
}

func TestLoadPipeConfigFrom_NoIdentity(t *testing.T) {
	path := writePipeYAML(t, t.TempDir(), `
name: test
description: test pipe
prompts:
  system: |
    You are a professional writer.
`)

	t.Setenv(EnvIdentity, "")

	pc, err := LoadPipeConfigFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(pc.Prompts.System, "Virgil") {
		t.Errorf("expected no identity injection, got: %s", pc.Prompts.System)
	}
}

func TestLoadPipeConfigFrom_EmptyPrompts(t *testing.T) {
	path := writePipeYAML(t, t.TempDir(), `
name: test
description: deterministic pipe
`)

	t.Setenv(EnvIdentity, "I am Virgil.")

	pc, err := LoadPipeConfigFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pc.Prompts.System != "" {
		t.Errorf("expected empty system prompt, got: %s", pc.Prompts.System)
	}
}

func TestLoadPipeConfigFrom_TemplatesOnly(t *testing.T) {
	path := writePipeYAML(t, t.TempDir(), `
name: test
description: test pipe
prompts:
  templates:
    formal: |
      Write formally.
`)

	t.Setenv(EnvIdentity, "I am Virgil.")

	pc, err := LoadPipeConfigFrom(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pc.Prompts.System != "" {
		t.Errorf("expected empty system prompt, got: %s", pc.Prompts.System)
	}

	if !strings.HasPrefix(pc.Prompts.Templates["formal"], "I am Virgil.") {
		t.Errorf("expected template to start with identity, got: %s", pc.Prompts.Templates["formal"])
	}
}
