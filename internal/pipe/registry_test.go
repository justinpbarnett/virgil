package pipe

import (
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	def := Definition{Name: "test", Description: "test pipe", Category: "general"}
	handler := func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		return envelope.New("test", "run")
	}

	r.Register(def, handler)

	h, ok := r.Get("test")
	if !ok {
		t.Fatal("expected handler to be found")
	}
	if h == nil {
		t.Fatal("expected handler to be non-nil")
	}

	result := h(envelope.New("input", "test"), nil)
	if result.Pipe != "test" {
		t.Errorf("expected pipe=test, got %s", result.Pipe)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected handler not to be found")
	}
}

func TestRegistryDefinitions(t *testing.T) {
	r := NewRegistry()

	noop := func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		return input
	}

	r.Register(Definition{Name: "a", Category: "general"}, noop)
	r.Register(Definition{Name: "b", Category: "memory"}, noop)
	r.Register(Definition{Name: "c", Category: "time"}, noop)

	defs := r.Definitions()
	if len(defs) != 3 {
		t.Errorf("expected 3 definitions, got %d", len(defs))
	}

	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, name := range []string{"a", "b", "c"} {
		if !names[name] {
			t.Errorf("expected definition %s to be present", name)
		}
	}
}

func TestRegistryGetDefinition(t *testing.T) {
	r := NewRegistry()
	def := Definition{Name: "memory", Description: "store and retrieve", Category: "memory"}
	r.Register(def, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		return input
	})

	d, ok := r.GetDefinition("memory")
	if !ok {
		t.Fatal("expected definition to be found")
	}
	if d.Description != "store and retrieve" {
		t.Errorf("expected description 'store and retrieve', got '%s'", d.Description)
	}
}
