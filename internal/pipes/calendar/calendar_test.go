package calendar

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/testutil"
)

type mockCalendarClient struct {
	events []Event
	err    error
}

func (m *mockCalendarClient) GetEvents(_ context.Context, _ string, _, _ time.Time) ([]Event, error) {
	return m.events, m.err
}

func TestCalendarReturnsEvents(t *testing.T) {
	client := &mockCalendarClient{
		events: []Event{
			{Title: "Standup", Start: "10:00", End: "10:30", Location: "Room A"},
			{Title: "Lunch", Start: "12:00", End: "13:00", Location: ""},
		},
	}

	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")
	result := handler(input, map[string]string{"range": "today"})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.ContentType != "list" {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}

	events, ok := result.Content.([]Event)
	if !ok {
		t.Fatalf("expected []Event, got %T", result.Content)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestCalendarEmptySchedule(t *testing.T) {
	client := &mockCalendarClient{events: []Event{}}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	events, ok := result.Content.([]Event)
	if !ok {
		t.Fatalf("expected []Event, got %T", result.Content)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestCalendarAPIError(t *testing.T) {
	client := &mockCalendarClient{err: fmt.Errorf("API rate limited")}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
}

func TestCalendarTimeoutErrorIsRetryable(t *testing.T) {
	client := &mockCalendarClient{err: context.DeadlineExceeded}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	if result.Error == nil {
		t.Fatal("expected error")
	}
	if !result.Error.Retryable {
		t.Error("expected retryable=true for timeout error")
	}
}

func TestCalendarNoClient(t *testing.T) {
	handler := NewHandler(nil, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{})

	testutil.AssertFatalError(t, result)
}

func TestCalendarDefaultRange(t *testing.T) {
	var receivedMin, receivedMax time.Time
	client := &mockCalendarClient{}

	// Override to capture range
	handler := func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		rangeFlag := flags["range"]
		if rangeFlag == "" {
			rangeFlag = "today"
		}
		receivedMin, receivedMax = resolveRange(rangeFlag)
		return NewHandler(client, nil)(input, flags)
	}

	input := envelope.New("input", "test")
	handler(input, map[string]string{})

	if receivedMin.IsZero() || receivedMax.IsZero() {
		t.Error("expected range to be resolved")
	}
	if receivedMax.Sub(receivedMin) != 24*time.Hour {
		t.Errorf("expected 24h range, got %v", receivedMax.Sub(receivedMin))
	}
}

func TestCalendarEnvelopeCompliance(t *testing.T) {
	client := &mockCalendarClient{
		events: []Event{{Title: "Test", Start: "10:00", End: "11:00", Location: ""}},
	}
	handler := NewHandler(client, nil)
	input := envelope.New("input", "test")

	result := handler(input, map[string]string{"range": "today"})

	testutil.AssertEnvelope(t, result, "calendar", "list")
	if result.Args == nil {
		t.Error("expected args to be non-nil")
	}
	if result.Content == nil {
		t.Error("expected content to be non-nil")
	}
	if result.ContentType != envelope.ContentList {
		t.Errorf("expected content_type=list, got %s", result.ContentType)
	}
	if result.Error != nil {
		t.Errorf("expected no error, got %v", result.Error)
	}
}
