package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func TestRunOneShotSuccess(t *testing.T) {
	env := envelope.New("chat", "respond")
	env.Content = "Hello! How can I help?"
	env.ContentType = "text"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/signal" {
			t.Errorf("expected /signal, got %s", r.URL.Path)
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["text"] != "hello" {
			t.Errorf("expected text=hello, got %s", req["text"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := RunOneShot("hello", addr)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "Hello! How can I help?") {
		t.Errorf("expected output to contain response, got: %s", string(out))
	}
}

func TestRunOneShotError(t *testing.T) {
	env := envelope.New("chat", "respond")
	env.Error = &envelope.EnvelopeError{
		Message:  "provider unavailable",
		Severity: "fatal",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	err := RunOneShot("hello", addr)

	if err == nil {
		t.Fatal("expected error for envelope with error field")
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected error message about provider, got: %v", err)
	}
}

func TestRunOneShotServerDown(t *testing.T) {
	err := RunOneShot("hello", "localhost:0")
	if err == nil {
		t.Fatal("expected error when server is not running")
	}
}

func TestRunOneShotListContent(t *testing.T) {
	env := envelope.New("memory", "retrieve")
	env.Content = []any{"item one", "item two", "item three"}
	env.ContentType = "list"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := RunOneShot("recall notes", addr)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, _ := io.ReadAll(r)
	output := string(out)
	if !strings.Contains(output, "1.") {
		t.Errorf("expected numbered list output, got: %s", output)
	}
}
