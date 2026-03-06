package runtime

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestStatusSinkEmitsTaskStatus(t *testing.T) {
	var mu sync.Mutex
	var events []StreamEvent

	sink := NewStatusSink(&mu, func(ev StreamEvent) {
		events = append(events, ev)
	})

	sink(StatusEvent{
		TaskID:   "t1",
		Type:     "status",
		Name:     "pricing-table",
		Pipe:     "build",
		Status:   "running",
		Activity: "edit pricing.go",
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "task_status" {
		t.Errorf("expected task_status, got %s", ev.Type)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload["task_id"] != "t1" {
		t.Errorf("expected task_id=t1, got %v", payload["task_id"])
	}
	if payload["status"] != "running" {
		t.Errorf("expected status=running, got %v", payload["status"])
	}
	if payload["activity"] != "edit pricing.go" {
		t.Errorf("expected activity=edit pricing.go, got %v", payload["activity"])
	}
}

func TestStatusSinkEmitsTaskChunk(t *testing.T) {
	var mu sync.Mutex
	var events []StreamEvent

	sink := NewStatusSink(&mu, func(ev StreamEvent) {
		events = append(events, ev)
	})

	sink(StatusEvent{
		TaskID: "t2",
		Type:   "chunk",
		Text:   "Reading the pricing table...",
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "task_chunk" {
		t.Errorf("expected task_chunk, got %s", ev.Type)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload["task_id"] != "t2" {
		t.Errorf("expected task_id=t2, got %s", payload["task_id"])
	}
	if payload["text"] != "Reading the pricing table..." {
		t.Errorf("unexpected text: %s", payload["text"])
	}
}

func TestStatusSinkEmitsTaskDone(t *testing.T) {
	var mu sync.Mutex
	var events []StreamEvent

	sink := NewStatusSink(&mu, func(ev StreamEvent) {
		events = append(events, ev)
	})

	sink(StatusEvent{
		TaskID:   "t3",
		Type:     "done",
		Status:   "done",
		Duration: 3100 * time.Millisecond,
	})

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "task_done" {
		t.Errorf("expected task_done, got %s", ev.Type)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload["status"] != "done" {
		t.Errorf("expected status=done, got %s", payload["status"])
	}
	if payload["duration"] == "" {
		t.Errorf("expected non-empty duration")
	}
}

func TestStatusSinkConcurrentSafety(t *testing.T) {
	var mu sync.Mutex
	var events []StreamEvent

	sink := NewStatusSink(&mu, func(ev StreamEvent) {
		events = append(events, ev)
	})

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			sink(StatusEvent{
				TaskID: "t1",
				Type:   "chunk",
				Text:   "chunk",
			})
			_ = i
		}(i)
	}
	wg.Wait()

	if len(events) != n {
		t.Errorf("expected %d events, got %d", n, len(events))
	}
}

func TestStatusSinkUnknownTypeIgnored(t *testing.T) {
	var mu sync.Mutex
	var events []StreamEvent

	sink := NewStatusSink(&mu, func(ev StreamEvent) {
		events = append(events, ev)
	})

	sink(StatusEvent{
		TaskID: "t1",
		Type:   "unknown",
	})

	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown type, got %d", len(events))
	}
}
