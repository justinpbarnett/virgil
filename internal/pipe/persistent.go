package pipe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

// PersistentProcess manages a long-lived pipe subprocess. Instead of spawning
// a new process per call, the process is started once and reused. Requests and
// responses are sent over persistent stdin/stdout using newline-delimited JSON,
// compatible with the existing SubprocessRequest / SubprocessChunk protocol.
//
// All calls to the process are serialized by a mutex to avoid interleaving.
// If the process crashes, it is automatically restarted on the next call.
type PersistentProcess struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	cfg     SubprocessConfig
	logger  *slog.Logger
	alive   bool
}

// NewPersistentProcess creates a PersistentProcess but does not start it.
// Call Start() before registering its Handler or StreamHandler.
func NewPersistentProcess(cfg SubprocessConfig) *PersistentProcess {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &PersistentProcess{
		cfg:    cfg,
		logger: cfg.Logger,
	}
}

// Start launches the subprocess with VIRGIL_PERSISTENT=1 and sets up the
// stdin writer and stdout scanner for use by Handler and StreamHandler.
func (p *PersistentProcess) Start() error {
	cmd := exec.Command(p.cfg.Executable)
	cmd.Dir = p.cfg.WorkDir
	// Append persistent flag so pipehost.Run enters loop mode
	cmd.Env = append(p.cfg.Env, "VIRGIL_PERSISTENT=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024) // up to 1 MB per line

	p.cmd = cmd
	p.stdin = stdin
	p.scanner = scanner
	p.alive = true
	return nil
}

// Stop closes stdin (signalling EOF to the child) and waits for it to exit.
func (p *PersistentProcess) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *PersistentProcess) stopLocked() {
	if !p.alive {
		return
	}
	p.alive = false
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		done := make(chan struct{})
		go func() {
			p.cmd.Wait() //nolint:errcheck
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			p.cmd.Process.Kill()
		}
	}
}

// restart stops the current process and starts a fresh one. Must be called
// with the mutex held.
func (p *PersistentProcess) restart() error {
	p.logger.Warn("restarting persistent pipe", "pipe", p.cfg.Name)
	p.stopLocked()
	if err := p.Start(); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	return nil
}

// writeRequest serialises req as a newline-terminated JSON line on stdin.
func (p *PersistentProcess) writeRequest(req SubprocessRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')
	_, err = p.stdin.Write(data)
	return err
}

// writeWithRetry writes a request, restarting the process on failure and
// retrying once. Must be called with the mutex held.
func (p *PersistentProcess) writeWithRetry(req SubprocessRequest) *envelope.Envelope {
	if err := p.writeRequest(req); err != nil {
		if err2 := p.restart(); err2 != nil {
			e := envelope.NewFatalError(p.cfg.Name, "write failed, restart failed: "+err.Error())
			return &e
		}
		if err3 := p.writeRequest(req); err3 != nil {
			e := envelope.NewFatalError(p.cfg.Name, "write failed after restart: "+err3.Error())
			return &e
		}
	}
	return nil
}

// Handler returns a Handler that sends sync requests to the persistent process.
func (p *PersistentProcess) Handler() Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		p.mu.Lock()
		defer p.mu.Unlock()

		req := SubprocessRequest{Envelope: input, Flags: flags, Stream: false}

		if errEnv := p.writeWithRetry(req); errEnv != nil {
			return *errEnv
		}

		if !p.scanner.Scan() {
			if err := p.restart(); err != nil {
				return envelope.NewFatalError(p.cfg.Name, "read failed, restart failed")
			}
			if err := p.writeRequest(req); err != nil {
				return envelope.NewFatalError(p.cfg.Name, "write after restart: "+err.Error())
			}
			if !p.scanner.Scan() {
				return envelope.NewFatalError(p.cfg.Name, "read failed after restart")
			}
		}

		var result envelope.Envelope
		if err := json.Unmarshal(p.scanner.Bytes(), &result); err != nil {
			return envelope.NewFatalError(p.cfg.Name, "unmarshal: "+err.Error())
		}
		return result
	}
}

// StreamHandler returns a StreamHandler that sends streaming requests to the
// persistent process. Chunk lines are forwarded to sink; the final envelope
// line terminates the response.
func (p *PersistentProcess) StreamHandler() StreamHandler {
	return func(ctx context.Context, input envelope.Envelope, flags map[string]string, sink func(chunk string)) envelope.Envelope {
		p.mu.Lock()
		defer p.mu.Unlock()

		req := SubprocessRequest{Envelope: input, Flags: flags, Stream: true}

		if errEnv := p.writeWithRetry(req); errEnv != nil {
			return *errEnv
		}

		for p.scanner.Scan() {
			// Check for context cancellation between lines
			select {
			case <-ctx.Done():
				// Drain the remaining response so the process stays in a clean state.
				for p.scanner.Scan() {
					var c SubprocessChunk
					if json.Unmarshal(p.scanner.Bytes(), &c) == nil && c.Envelope != nil {
						break
					}
				}
				return envelope.NewRetryableError(p.cfg.Name, "context cancelled")
			default:
			}

			var chunk SubprocessChunk
			if err := json.Unmarshal(p.scanner.Bytes(), &chunk); err != nil {
				continue
			}
			if chunk.Envelope != nil {
				return *chunk.Envelope
			}
			if chunk.Chunk != "" {
				sink(chunk.Chunk)
			}
		}

		// Scanner stopped — process likely died
		if err := p.restart(); err != nil {
			return envelope.NewFatalError(p.cfg.Name, "process died, restart failed")
		}
		return envelope.NewFatalError(p.cfg.Name, "process closed without response")
	}
}
