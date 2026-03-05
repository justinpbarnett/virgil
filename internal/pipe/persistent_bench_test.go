package pipe_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/justinpbarnett/virgil/internal/pipe"
)

// buildEchoHelperForBench compiles the echo helper binary for benchmarking.
// Returns the path to the binary or "" if compilation fails (bench is skipped).
func buildEchoHelperForBench(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()

	src := filepath.Join(dir, "main.go")
	srcContent := `package main

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
				if err == io.EOF { return }
				os.Exit(1)
			}
			out := envelope.New("echo", "response")
			out.Content = req.Envelope.Content
			out.ContentType = envelope.ContentText
			enc.Encode(out)
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
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		b.Skipf("cannot write helper src: %v", err)
		return ""
	}

	binPath := filepath.Join(dir, "echo-helper")
	projectRoot, _ := filepath.Abs("../..")
	cmd := exec.Command("go", "build", "-o", binPath, src)
	cmd.Dir = projectRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Skipf("cannot build echo helper: %v\n%s", err, out)
		return ""
	}
	return binPath
}

func BenchmarkPersistentHandler(b *testing.B) {
	binPath := buildEchoHelperForBench(b)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	proc := pipe.NewPersistentProcess(cfg)
	if err := proc.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer proc.Stop()

	h := proc.Handler()
	seed := makeSeed("benchmark payload")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h(seed, nil)
	}
}

func BenchmarkSubprocessHandler(b *testing.B) {
	binPath := buildEchoHelperForBench(b)

	cfg := pipe.SubprocessConfig{
		Name:       "echo",
		Executable: binPath,
		Timeout:    5 * time.Second,
		Env:        os.Environ(),
	}
	h := pipe.SubprocessHandler(cfg)
	seed := makeSeed("benchmark payload")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h(seed, nil)
	}
}
