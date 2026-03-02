package runtime

import (
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

type testObserver struct {
	transitions []string
}

func (o *testObserver) OnTransition(p string, _ envelope.Envelope, _ time.Duration) {
	o.transitions = append(o.transitions, p)
}

func TestExecuteSinglePipe(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "echo"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("echo", "run")
		out.Content = input.Content
		out.ContentType = "text"
		return out
	})

	obs := &testObserver{}
	rt := New(reg, obs, nil)

	seed := envelope.New("input", "test")
	seed.Content = "hello"

	result := rt.Execute(Plan{Steps: []Step{{Pipe: "echo"}}}, seed)

	if result.Pipe != "echo" {
		t.Errorf("expected pipe=echo, got %s", result.Pipe)
	}
	if result.Content != "hello" {
		t.Errorf("expected content=hello, got %v", result.Content)
	}
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 transition, got %d", len(obs.transitions))
	}
}

func TestExecuteTwoPipeChain(t *testing.T) {
	reg := pipe.NewRegistry()
	reg.Register(pipe.Definition{Name: "upper"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("upper", "transform")
		s, _ := input.Content.(string)
		out.Content = s + " UPPER"
		out.ContentType = "text"
		return out
	})
	reg.Register(pipe.Definition{Name: "wrap"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("wrap", "transform")
		s, _ := input.Content.(string)
		out.Content = "[" + s + "]"
		out.ContentType = "text"
		return out
	})

	rt := New(reg, nil, nil)

	seed := envelope.New("input", "test")
	seed.Content = "hello"

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "upper"},
		{Pipe: "wrap"},
	}}, seed)

	if result.Content != "[hello UPPER]" {
		t.Errorf("expected '[hello UPPER]', got '%v'", result.Content)
	}
}

func TestExecuteThreePipeChain(t *testing.T) {
	reg := pipe.NewRegistry()

	makePipe := func(name, suffix string) {
		reg.Register(pipe.Definition{Name: name}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
			out := envelope.New(name, "run")
			s, _ := input.Content.(string)
			out.Content = s + suffix
			out.ContentType = "text"
			return out
		})
	}

	makePipe("a", "-A")
	makePipe("b", "-B")
	makePipe("c", "-C")

	obs := &testObserver{}
	rt := New(reg, obs, nil)

	seed := envelope.New("input", "test")
	seed.Content = "start"

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "a"}, {Pipe: "b"}, {Pipe: "c"},
	}}, seed)

	if result.Content != "start-A-B-C" {
		t.Errorf("expected 'start-A-B-C', got '%v'", result.Content)
	}
	if len(obs.transitions) != 3 {
		t.Errorf("expected 3 transitions, got %d", len(obs.transitions))
	}
}

func TestExecuteFatalErrorHalts(t *testing.T) {
	reg := pipe.NewRegistry()

	reg.Register(pipe.Definition{Name: "fail"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("fail", "run")
		out.Error = &envelope.EnvelopeError{
			Message:  "something broke",
			Severity: "fatal",
		}
		return out
	})
	reg.Register(pipe.Definition{Name: "after"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("after", "run")
		out.Content = "should not reach"
		return out
	})

	obs := &testObserver{}
	rt := New(reg, obs, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "fail"}, {Pipe: "after"},
	}}, envelope.New("input", "test"))

	if result.Pipe != "fail" {
		t.Errorf("expected pipe=fail, got %s", result.Pipe)
	}
	if result.Error == nil {
		t.Fatal("expected error")
	}
	if len(obs.transitions) != 1 {
		t.Errorf("expected 1 transition (halted), got %d", len(obs.transitions))
	}
}

func TestExecuteWarnContinues(t *testing.T) {
	reg := pipe.NewRegistry()

	reg.Register(pipe.Definition{Name: "warn"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("warn", "run")
		out.Content = "partial"
		out.ContentType = "text"
		out.Error = &envelope.EnvelopeError{
			Message:  "partial results",
			Severity: "warn",
		}
		return out
	})
	reg.Register(pipe.Definition{Name: "next"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("next", "run")
		s, _ := input.Content.(string)
		out.Content = s + " + more"
		out.ContentType = "text"
		return out
	})

	rt := New(reg, nil, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "warn"}, {Pipe: "next"},
	}}, envelope.New("input", "test"))

	if result.Content != "partial + more" {
		t.Errorf("expected 'partial + more', got '%v'", result.Content)
	}
}

func TestExecuteMissingPipe(t *testing.T) {
	reg := pipe.NewRegistry()
	rt := New(reg, nil, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "nonexistent"},
	}}, envelope.New("input", "test"))

	if result.Error == nil {
		t.Fatal("expected error for missing pipe")
	}
	if result.Error.Severity != "fatal" {
		t.Errorf("expected fatal severity, got %s", result.Error.Severity)
	}
}

func TestExecuteFlagsPassedToPipe(t *testing.T) {
	reg := pipe.NewRegistry()
	var receivedFlags map[string]string

	reg.Register(pipe.Definition{Name: "flagtest"}, func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		receivedFlags = flags
		return envelope.New("flagtest", "run")
	})

	rt := New(reg, nil, nil)

	result := rt.Execute(Plan{Steps: []Step{
		{Pipe: "flagtest", Flags: map[string]string{"action": "store", "key": "value"}},
	}}, envelope.New("input", "test"))

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if receivedFlags["action"] != "store" {
		t.Errorf("expected action=store, got %s", receivedFlags["action"])
	}
	if receivedFlags["key"] != "value" {
		t.Errorf("expected key=value, got %s", receivedFlags["key"])
	}
}
