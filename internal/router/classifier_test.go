package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

type mockProvider struct {
	response string
	err      error
	called   bool
}

func (m *mockProvider) Complete(_ context.Context, _, _ string) (string, error) {
	m.called = true
	return m.response, m.err
}

func classifierDefs() []pipe.Definition {
	return []pipe.Definition{
		{Name: "calendar", Description: "Manage calendar events and schedules"},
		{Name: "draft", Description: "Compose and draft messages"},
		{Name: "memory", Description: "Remember and recall information"},
		{Name: "chat", Description: "General conversation"},
	}
}

func TestClassifyBuildsCatalogue(t *testing.T) {
	c := NewClassifier(&mockProvider{}, classifierDefs(), nil)

	if strings.Contains(c.catalogue, "chat") {
		t.Error("catalogue should not include chat pipe")
	}
	if !strings.Contains(c.catalogue, "calendar") {
		t.Error("catalogue should include calendar pipe")
	}
	if !strings.Contains(c.catalogue, "draft") {
		t.Error("catalogue should include draft pipe")
	}
	if !strings.Contains(c.catalogue, "memory") {
		t.Error("catalogue should include memory pipe")
	}
}

func TestClassifyMatchesPipe(t *testing.T) {
	provider := &mockProvider{response: "calendar"}
	c := NewClassifier(provider, classifierDefs(), nil)

	got, conf := c.Classify(context.Background(), "when is my next meeting?")
	if got != "calendar" {
		t.Errorf("expected calendar, got %s", got)
	}
	if conf != 0.7 {
		t.Errorf("expected confidence 0.7, got %f", conf)
	}
}

func TestClassifyReturnsChat(t *testing.T) {
	provider := &mockProvider{response: "chat"}
	c := NewClassifier(provider, classifierDefs(), nil)

	got, conf := c.Classify(context.Background(), "hello there")
	if got != "chat" {
		t.Errorf("expected chat, got %s", got)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0, got %f", conf)
	}
}

func TestClassifyUnrecognizedResponse(t *testing.T) {
	provider := &mockProvider{response: "garbage_pipe_name"}
	c := NewClassifier(provider, classifierDefs(), nil)

	got, conf := c.Classify(context.Background(), "some signal")
	if got != "chat" {
		t.Errorf("expected chat for unrecognized response, got %s", got)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0 for unrecognized response, got %f", conf)
	}
}

func TestClassifyProviderError(t *testing.T) {
	provider := &mockProvider{err: errors.New("provider failed")}
	c := NewClassifier(provider, classifierDefs(), nil)

	got, conf := c.Classify(context.Background(), "some signal")
	if got != "chat" {
		t.Errorf("expected chat on provider error, got %s", got)
	}
	if conf != 0.0 {
		t.Errorf("expected confidence 0.0 on provider error, got %f", conf)
	}
}
