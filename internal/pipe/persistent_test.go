package pipe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
)

// buildEchoHelper compiles a tiny helper binary that echoes SubprocessRequests
// back as envelopes. Returns the binary path and a cleanup func.
func buildEchoHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	src := filepath.Join(dir, "main.go")
	src_content := `package main

import (
	"encoding/json"
	"io"
	"os"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

func main() {
	if os.Getenv("VIRGIL_PERSISTENT") == "1" {
		dec := json.NewDecoder(os.Stdin)
		enc := json.NewEncoder(os.Stdout)
		for {
			var req pipe.SubprocessRequest
			if err := dec.Decode(&req); err != nil {
				if err == io.EOF {
					return
				}
				os.Exit(1)
			}
			out := envelope.New("echo", "response")
			out.Content = req.Envelope.Content
			out.ContentType = envelope.ContentText
			if req.Stream {
				enc.Encode(pipe.SubprocessChunk{Chunk: "chunk1"})
				enc.Encode(pipe.SubprocessChunk{Envelope: &out})
			} else {
				enc.Encode(out)
			}
		}
	} else {
		var req pipe.SubprocessRequest
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			os.Exit(1)
		}
		out := envelope.New("echo", "response")
		out.Content = req.Envelope.Content
		out.ContentType = envelope.ContentText
		json.NewEncoder(os.Stdout).Encode(out)
	}
}
`
	if err := os.WriteFile(src, []byte(src_content), 0o644); err != nil {
		t.Fatalf("write helper src: %v", err)
	}

	binPath := filepath.Join(dir, "echo-helper")
	cmd := exec.Command("go", "build", "-o", binPath, src)
	cmd.Dir = filepath.Join(os.Getenv("GOPATH"), "src") // may be empty; use module root
	// Use the project root for the build so imports resolve
	projectRoot, _ := filepath.Abs("../..")
	cmd.Dir = projectRoot
	cmd.Args = append(cmd.Args[:0], "go", "build", "-o", binPath, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("cannot build echo helper (build env unavailable): %v\n%s", err, out)
	}
	return binPath
}

func makeSeed(text string) envelope.Envelope {
	e := envelope.New("test", "signal")
	e.Content = text
	e.ContentType = envelope.ContentText
	return e
}

func TestPersistentProcessStartStop(t *testing.T) {
	binPath := buildEchoHelper(t)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	proc.Stop()
}

func TestPersistentHandlerRoundtrip(t *testing.T) {
	binPath := buildEchoHelper(t)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Stop()

	h := proc.Handler()
	seed := makeSeed("hello persistent")
	result := h(seed, nil)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	got := fmt.Sprintf("%v", result.Content)
	if got != "hello persistent" {
		t.Errorf("expected content %q, got %q", "hello persistent", got)
	}
}

func TestPersistentHandlerMultipleCalls(t *testing.T) {
	binPath := buildEchoHelper(t)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Stop()

	h := proc.Handler()
	for i := 0; i < 5; i++ {
		msg := fmt.Sprintf("message %d", i)
		result := h(makeSeed(msg), nil)
		if result.Error != nil {
			t.Fatalf("call %d: unexpected error: %v", i, result.Error.Message)
		}
		got := fmt.Sprintf("%v", result.Content)
		if got != msg {
			t.Errorf("call %d: expected %q, got %q", i, msg, got)
		}
	}
}

func TestPersistentStreamHandler(t *testing.T) {
	binPath := buildEchoHelper(t)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Stop()

	sh := proc.StreamHandler()
	var chunks []string
	result := sh(context.Background(), makeSeed("stream test"), nil, func(c string) {
		chunks = append(chunks, c)
	})
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error.Message)
	}
	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}
}

// TestPersistentStreamHandlerRetriesAfterCrash verifies that the StreamHandler
// restarts the process and retries the request when the subprocess dies mid-stream.
// Regression test: previously StreamHandler restarted but returned a fatal error
// without resending the request ("process closed without response").
func TestPersistentStreamHandlerRetriesAfterCrash(t *testing.T) {
	binPath := buildCrashOnceHelper(t)

	cfg := pipe.SubprocessConfig{
		Name:       "crash-once",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer proc.Stop()

	sh := proc.StreamHandler()
	var chunks []string
	result := sh(context.Background(), makeSeed("retry test"), nil, func(c string) {
		chunks = append(chunks, c)
	})
	if result.Error != nil {
		t.Fatalf("expected successful retry, got error: %v", result.Error.Message)
	}
	got := fmt.Sprintf("%v", result.Content)
	if got != "retry test" {
		t.Errorf("expected content %q, got %q", "retry test", got)
	}
}

// buildCrashOnceHelper compiles a helper binary that exits immediately on the
// first stream request (simulating a crash) but works normally after restart.
// Uses a marker file to track whether it has crashed before.
func buildCrashOnceHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "crashed")

	src := filepath.Join(dir, "main.go")
	srcContent := fmt.Sprintf(`package main

import (
	"encoding/json"
	"io"
	"os"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/envelope"
)

const markerPath = %q

func main() {
	if os.Getenv("VIRGIL_PERSISTENT") != "1" {
		os.Exit(1)
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req pipe.SubprocessRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			os.Exit(1)
		}
		// First stream request: crash (exit without response)
		if req.Stream {
			if _, err := os.Stat(markerPath); os.IsNotExist(err) {
				os.WriteFile(markerPath, []byte("1"), 0o644)
				os.Exit(0) // simulate crash
			}
		}
		out := envelope.New("crash-once", "response")
		out.Content = req.Envelope.Content
		out.ContentType = envelope.ContentText
		if req.Stream {
			enc.Encode(pipe.SubprocessChunk{Chunk: "chunk1"})
			enc.Encode(pipe.SubprocessChunk{Envelope: &out})
		} else {
			enc.Encode(out)
		}
	}
}
`, markerPath)
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatalf("write crash-once src: %v", err)
	}

	binPath := filepath.Join(dir, "crash-once-helper")
	projectRoot, _ := filepath.Abs("../..")
	cmd := exec.Command("go", "build", "-o", binPath, src)
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("cannot build crash-once helper (build env unavailable): %v\n%s", err, out)
	}
	return binPath
}

func TestPersistentHandlerMarshalledRequest(t *testing.T) {
	// Verify the SubprocessRequest is correctly marshalled to JSON
	req := pipe.SubprocessRequest{
		Envelope: makeSeed("test"),
		Flags:    map[string]string{"key": "val"},
		Stream:   false,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out pipe.SubprocessRequest
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Flags["key"] != "val" {
		t.Errorf("flag not preserved: %v", out.Flags)
	}
}
