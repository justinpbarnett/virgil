package voice

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Recorder captures microphone audio using sox's rec command.
type Recorder struct {
	cmd  *exec.Cmd
	path string
}

// StartRecording starts capturing audio to a temp WAV file (16kHz, mono, 16-bit).
func StartRecording() (*Recorder, error) {
	f, err := os.CreateTemp("", "virgil-rec-*.wav")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	f.Close()

	cmd := exec.Command("rec", "-q", "-r", "16000", "-c", "1", "-b", "16", path)
	if err := cmd.Start(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("starting rec: %w (is sox installed? brew install sox)", err)
	}

	return &Recorder{cmd: cmd, path: path}, nil
}

// Stop sends SIGINT to sox for a clean stop and returns the path to the WAV file.
func (r *Recorder) Stop() (string, error) {
	if r.cmd.Process == nil {
		return "", fmt.Errorf("recording process not started")
	}
	if err := r.cmd.Process.Signal(syscall.SIGINT); err != nil {
		r.cmd.Process.Kill()
	}
	r.cmd.Wait()
	return r.path, nil
}

// PlayAudio plays an audio file asynchronously using afplay on macOS.
// It cleans up the file after playback completes.
func PlayAudio(path string) error {
	afplay, err := exec.LookPath("afplay")
	if err != nil {
		return fmt.Errorf("afplay not found: %w", err)
	}
	cmd := exec.Command(afplay, path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting afplay: %w", err)
	}
	go func() {
		cmd.Wait()
		os.Remove(path)
	}()
	return nil
}

// PlayAudioSync plays an audio file synchronously, blocking until complete.
// Returns whether playback is still in progress (for overlap detection).
func PlayAudioSync(path string) error {
	afplay, err := exec.LookPath("afplay")
	if err != nil {
		return fmt.Errorf("afplay not found: %w", err)
	}
	cmd := exec.Command(afplay, path)
	defer os.Remove(path)
	return cmd.Run()
}
