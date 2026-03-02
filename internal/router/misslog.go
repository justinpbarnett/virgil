package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type MissEntry struct {
	Signal           string   `json:"signal"`
	KeywordsFound    []string `json:"keywords_found"`
	KeywordsNotFound []string `json:"keywords_not_found"`
	FallbackPipe     string   `json:"fallback_pipe"`
	Timestamp        string   `json:"timestamp"`
}

type MissLog struct {
	mu   sync.Mutex
	file *os.File
}

func NewMissLog(path string) (*MissLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating miss log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening miss log: %w", err)
	}
	return &MissLog{file: f}, nil
}

func (m *MissLog) Log(entry MissEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = m.file.Write(append(data, '\n'))
	return err
}

func (m *MissLog) Close() error {
	return m.file.Close()
}
