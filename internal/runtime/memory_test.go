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
	calls []struct {
		pipe, signal, output string
		contextIDs           []string
	}
	err  error
	done chan struct{} // closed after each SaveInvocation call
}

func newStubSaver() *stubSaver {
	return &stubSaver{done: make(chan struct{}, 1)}
}

func (s *stubSaver) SaveInvocation(pipe, signal, output string, contextIDs []string) error {
	if s.err != nil {
		select {
		case s.done <- struct{}{}:
		default:
		}
		return s.err
	}
	s.calls = append(s.calls, struct {
		pipe, signal, output string
		contextIDs           []string
	}{pipe, signal, output, contextIDs})
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

// waitCall blocks until SaveInvocation has been called or the timeout elapses.
func (s *stubSaver) waitCall(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for SaveInvocation to be called")
	}
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

	saver := newStubSaver()
	rt := New(reg, nil, nil)
	rt.WithMemory(nil, saver, nil)

	seed := envelope.New("input", "test")
	seed.Content = "hello world"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)
	saver.waitCall(t)

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

func TestAutoSavePassesContextIDs(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, echoHandler("echo"))

	saver := newStubSaver()
	inj := &stubInjector{entries: []envelope.MemoryEntry{
		{ID: "mem-id-1", Type: "topic_history", Content: "previous session"},
		{ID: "mem-id-2", Type: "working_state", Content: "current state"},
		{ID: "", Type: "user_preferences", Content: "no id entry"},
	}}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, saver, map[string]config.MemoryConfig{})

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)
	saver.waitCall(t)

	if len(saver.calls) != 1 {
		t.Fatalf("expected 1 save call, got %d", len(saver.calls))
	}

	contextIDs := saver.calls[0].contextIDs
	if len(contextIDs) != 2 {
		t.Errorf("expected 2 context IDs (non-empty IDs only), got %d: %v", len(contextIDs), contextIDs)
	}
	hasID1 := false
	hasID2 := false
	for _, id := range contextIDs {
		if id == "mem-id-1" {
			hasID1 = true
		}
		if id == "mem-id-2" {
			hasID2 = true
		}
	}
	if !hasID1 || !hasID2 {
		t.Errorf("expected context IDs to include mem-id-1 and mem-id-2, got %v", contextIDs)
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

func TestStoreMemoryInjectorPopulatesID(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	s.PutState("project", "active", "working on something")
	s.SaveInvocation("educate", "teach me Go", "What do you know?")

	inj := NewStoreMemoryInjector(s)
	cfg := config.MemoryConfig{
		Context: []config.MemoryContextEntry{
			{Type: "working_state"},
			{Type: "topic_history"},
		},
		Budget: 500,
	}

	env := envelope.New("educate", "run")
	env.Content = "teach me Go"
	env.ContentType = envelope.ContentText

	result := inj.InjectContext(env, cfg)

	for _, m := range result.Memory {
		if m.ID == "" && m.Type != "user_preferences" {
			t.Errorf("expected non-empty ID for memory type %q", m.Type)
		}
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
	if err := saver.SaveInvocation("educate", "teach me Go", "What do you know?", nil); err != nil {
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

func TestStoreMemorySaverCreatesEdges(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Create two context memories
	ctx1, _ := s.SaveInvocation("educate", "teach Go", "answer 1")
	ctx2, _ := s.SaveInvocation("educate", "teach channels", "answer 2")

	saver := NewStoreMemorySaver(s)
	if err := saver.SaveInvocation("chat", "hello", "response", []string{ctx1, ctx2}); err != nil {
		t.Fatalf("SaveInvocation with contextIDs: %v", err)
	}

	// co_occurred edge should connect ctx1 and ctx2
	connected, err := s.TraverseFrom([]string{ctx1}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom: %v", err)
	}
	found := false
	for _, m := range connected {
		if m.ID == ctx2 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ctx2 to be reachable from ctx1 via co_occurred edge")
	}

	// produced_by edges: new invocation should be reachable from ctx1
	connected, err = s.TraverseFrom([]string{ctx1}, []string{"produced_by"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom produced_by: %v", err)
	}
	if len(connected) == 0 {
		t.Error("expected new invocation to be reachable via produced_by from context")
	}
}

// TestSkipFirstMemoryInjectionEquivalentToSequential verifies that pre-injecting
// the seed before Execute (parallel prefetch path) produces identical memory
// entries to the sequential path where the runtime injects memory internally.
func TestSkipFirstMemoryInjectionEquivalentToSequential(t *testing.T) {
	entries := []envelope.MemoryEntry{
		{Type: "topic_history", Content: "previous session"},
	}

	var seqMemory []envelope.MemoryEntry
	var preMemory []envelope.MemoryEntry

	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "capture"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("capture", "run")
		out.Content = input.Content
		out.ContentType = envelope.ContentText
		out.Memory = input.Memory
		return out
	})

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	// Sequential path: runtime injects memory internally.
	inj1 := &stubInjector{entries: entries}
	rt1 := New(reg, nil, nil)
	rt1.WithMemory(inj1, nil, map[string]config.MemoryConfig{})
	r1 := rt1.Execute(Plan{Steps: []Step{{Pipe: "capture"}}}, seed)
	seqMemory = r1.Memory

	// Parallel path: caller pre-injects the seed and sets SkipFirstMemoryInjection.
	inj2 := &stubInjector{entries: entries}
	preSeed := inj2.InjectContext(seed, config.MemoryConfig{})
	rt2 := New(reg, nil, nil)
	rt2.WithMemory(inj2, nil, map[string]config.MemoryConfig{})
	r2 := rt2.Execute(Plan{Steps: []Step{{Pipe: "capture"}}, SkipFirstMemoryInjection: true}, preSeed)
	preMemory = r2.Memory

	if len(seqMemory) != len(preMemory) {
		t.Fatalf("sequential got %d memory entries, parallel got %d", len(seqMemory), len(preMemory))
	}
	for i := range seqMemory {
		if seqMemory[i].Type != preMemory[i].Type || seqMemory[i].Content != preMemory[i].Content {
			t.Errorf("memory[%d] mismatch: seq=%+v, parallel=%+v", i, seqMemory[i], preMemory[i])
		}
	}
}

// TestSkipFirstMemoryInjectionSkipsInjector verifies that when
// SkipFirstMemoryInjection is set, the runtime does not call the injector for
// step 0 (preventing double-injection).
func TestSkipFirstMemoryInjectionSkipsInjector(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "noop"}, echoHandler("noop"))

	inj := &stubInjector{}
	rt := New(reg, nil, nil)
	rt.WithMemory(inj, nil, map[string]config.MemoryConfig{})

	seed := envelope.New("input", "test")
	seed.Content = "hello"
	seed.ContentType = envelope.ContentText

	rt.Execute(Plan{Steps: []Step{{Pipe: "noop"}}, SkipFirstMemoryInjection: true}, seed)

	if inj.called != 0 {
		t.Errorf("expected injector not called when SkipFirstMemoryInjection=true, got %d calls", inj.called)
	}
}
