package runtime

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/store"
)

// stubInjector tracks calls and optionally injects entries.
type stubInjector struct {
	called  int
	entries []envelope.MemoryEntry
}

func (s *stubInjector) InjectContext(env envelope.Envelope, cfg config.MemoryConfig) envelope.Envelope {
	s.called++
	if cfg.Disabled {
		return env
	}
	env.Memory = append(env.Memory, s.entries...)
	return env
}

// stubSaver records SaveInvocation calls.
type stubSaver struct {
	calls []struct{ pipe, signal, output string }
	err   error
}

func (s *stubSaver) SaveInvocation(pipe, signal, output string) error {
	if s.err != nil {
		return s.err
	}
	s.calls = append(s.calls, struct{ pipe, signal, output string }{pipe, signal, output})
	return nil
}

func echoHandler(name string) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New(name, "run")
		out.Content = input.Content
		out.ContentType = envelope.ContentText
		return out
	}
}

func TestInjectorCalledBeforeEachStep(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "a"}, echoHandler("a"))
	reg.Register(pipe.Definition{Name: "b"}, echoHandler("b"))

	inj := &stubInjector{}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, nil, map[string]config.MemoryConfig{})

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "a"}, {Pipe: "b"}}}, seed)

	if inj.called != 2 {
		t.Errorf("expected injector called 2 times, got %d", inj.called)
	}
}

func TestInjectorPopulatesMemoryField(t *testing.T) {
	var receivedMemory []envelope.MemoryEntry

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "reader"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		receivedMemory = input.Memory
		out := envelope.New("reader", "run")
		out.Content = "done"
		out.ContentType = envelope.ContentText
		return out
	})

	inj := &stubInjector{entries: []envelope.MemoryEntry{
		{Type: "topic_history", Content: "previous Go session"},
	}}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, nil, map[string]config.MemoryConfig{})

	seed := envelope.New("input", "test")
	seed.Content = "teach me Go"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "reader"}}}, seed)

	if len(receivedMemory) == 0 {
		t.Fatal("expected memory to be populated")
	}
	if receivedMemory[0].Type != "topic_history" {
		t.Errorf("expected type=topic_history, got %s", receivedMemory[0].Type)
	}
}

func TestInjectorDisabledSkipsInjection(t *testing.T) {
	var receivedMemory []envelope.MemoryEntry

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "noop"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		receivedMemory = input.Memory
		out := envelope.New("noop", "run")
		out.Content = "done"
		out.ContentType = envelope.ContentText
		return out
	})

	inj := &stubInjector{entries: []envelope.MemoryEntry{
		{Type: "topic_history", Content: "some context"},
	}}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, nil, map[string]config.MemoryConfig{
		"noop": {Disabled: true},
	})

	seed := envelope.New("input", "test")
	seed.Content = "check calendar"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "noop"}}}, seed)

	if len(receivedMemory) != 0 {
		t.Errorf("expected empty memory for disabled pipe, got %d entries", len(receivedMemory))
	}
}

func TestAutoSaveFiredAfterSuccess(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, echoHandler("echo"))

	saver := &stubSaver{}
	rt := New(reg, nil, nil)
	rt.WithMemory(nil, saver, nil)

	seed := envelope.New("input", "test")
	seed.Content = "hello world"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)

	if len(saver.calls) != 1 {
		t.Fatalf("expected 1 save call, got %d", len(saver.calls))
	}
	if saver.calls[0].pipe != "echo" {
		t.Errorf("expected pipe=echo, got %s", saver.calls[0].pipe)
	}
	if saver.calls[0].signal != "hello world" {
		t.Errorf("expected signal='hello world', got %s", saver.calls[0].signal)
	}
}

func TestAutoSaveNotFiredOnFatalError(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "fail"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("fail", "run")
		out.Error = envelope.FatalError("something broke")
		return out
	})

	saver := &stubSaver{}
	rt := New(reg, nil, nil)
	rt.WithMemory(nil, saver, nil)

	rt.Execute(Plan{Steps: []Step{{Pipe: "fail"}}}, envelope.New("input", "test"))

	if len(saver.calls) != 0 {
		t.Errorf("expected 0 save calls on fatal error, got %d", len(saver.calls))
	}
}

func TestAutoSaveFailureDoesNotPropagateToResult(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, echoHandler("echo"))

	saver := &stubSaver{err: errors.New("db unavailable")}
	rt := New(reg, nil, nil)
	rt.WithMemory(nil, saver, nil)

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	result := rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)

	if result.Error != nil {
		t.Errorf("expected no error when auto-save fails, got %v", result.Error)
	}
}

func TestNilInjectorAndSaverAreNoops(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, echoHandler("echo"))

	rt := New(reg, nil, nil)
	// No WithMemory call — injector and saver are nil

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	result := rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)

	if result.Error != nil {
		t.Errorf("expected no error with nil injector/saver, got %v", result.Error)
	}
	if result.Content != "hello" {
		t.Errorf("expected content=hello, got %v", result.Content)
	}
}

func TestDefaultMemoryConfigApplied(t *testing.T) {
	var receivedCfg config.MemoryConfig

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "pipe"}, echoHandler("pipe"))

	inj := &captureConfigInjector{}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, nil, map[string]config.MemoryConfig{})

	seed := envelope.New("input", "test")
	seed.Content = "test"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "pipe"}}}, seed)

	receivedCfg = inj.lastCfg
	// Config for unregistered pipe should be zero value (Budget=0, no Context)
	// The injector's InjectContext handles defaults internally
	if receivedCfg.Disabled {
		t.Error("expected non-disabled config for unregistered pipe")
	}
}

// captureConfigInjector records the last config it received.
type captureConfigInjector struct {
	lastCfg config.MemoryConfig
}

func (c *captureConfigInjector) InjectContext(env envelope.Envelope, cfg config.MemoryConfig) envelope.Envelope {
	c.lastCfg = cfg
	return env
}

func TestStoreMemoryInjectorIntegration(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	s.PutState("project", "active", "working on memory refactor")

	inj := NewStoreMemoryInjector(s)
	cfg := config.MemoryConfig{
		Context: []config.MemoryContextEntry{{Type: "working_state"}},
		Budget:  500,
	}

	env := envelope.New("educate", "run")
	env.Content = "teach me Go"
	env.ContentType = envelope.ContentText

	result := inj.InjectContext(env, cfg)

	if len(result.Memory) == 0 {
		t.Fatal("expected memory to be populated")
	}
	found := false
	for _, m := range result.Memory {
		if m.Type == "working_state" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected working_state entry in memory")
	}
}

func TestStoreMemorySaverIntegration(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	saver := NewStoreMemorySaver(s)
	if err := saver.SaveInvocation("educate", "teach me Go", "What do you know?"); err != nil {
		t.Fatalf("SaveInvocation: %v", err)
	}

	results, err := s.SearchInvocations("Go", "", 10, time.Time{})
	if err != nil {
		t.Fatalf("SearchInvocations: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected saved invocation to be searchable")
	}
}
