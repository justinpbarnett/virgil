package tui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/justinpbarnett/virgil/internal/config"
)

func EnsureServer(binary string, serverAddr string) error {
	dataDir := config.DataDir()
	pidPath := filepath.Join(dataDir, "virgil.pid")
	lockPath := filepath.Join(dataDir, "virgil.lock")

	os.MkdirAll(dataDir, 0o755)

	// Acquire file lock
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer lockFile.Close()
	defer os.Remove(lockPath)

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Check if server is already running
	if isServerRunning(pidPath, serverAddr) {
		return nil
	}

	// Start server — send output to log file, not the terminal
	logPath := ServerLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening server log: %w", err)
	}

	cmd := exec.Command(binary, "--server")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting server: %w", err)
	}
	logFile.Close() // subprocess inherited the FD; parent can close its copy

	// Wait for server to be healthy
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://%s/health", serverAddr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("server failed to start within 5 seconds")
}

// ServerLogPath returns the path to the server log file.
func ServerLogPath() string {
	return filepath.Join(config.DataDir(), "virgil.log")
}

func isServerRunning(pidPath, serverAddr string) bool {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	// Verify it's responding
	resp, err := http.Get(fmt.Sprintf("http://%s/health", serverAddr))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
