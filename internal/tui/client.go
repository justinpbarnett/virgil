package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

var signalClient = &http.Client{
	Timeout: 30 * time.Second,
}

var streamClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		IdleConnTimeout: 90 * time.Second,
	},
	// No total timeout — SSE streams are long-lived.
}

func signalURL(serverAddr string) string {
	return fmt.Sprintf("http://%s/signal", serverAddr)
}

func signalBody(text string) io.Reader {
	body, _ := json.Marshal(map[string]string{"text": text})
	return bytes.NewReader(body)
}

func postSignal(serverAddr, text string) (envelope.Envelope, error) {
	resp, err := signalClient.Post(signalURL(serverAddr), "application/json", signalBody(text))
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return envelope.Envelope{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return envelope.Envelope{}, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(data))
	}

	var env envelope.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return envelope.Envelope{}, fmt.Errorf("invalid response: %w", err)
	}

	return env, nil
}

type sseReader struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
}

func openSSEStream(ctx context.Context, serverAddr, text string) (*sseReader, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", signalURL(serverAddr), signalBody(text))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", envelope.SSEContentType)

	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, string(data))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024) // 1 MB max line for large envelopes
	return &sseReader{
		scanner: scanner,
		body:    resp.Body,
	}, nil
}

type sseEvent struct {
	Type string
	Data string
}

func (r *sseReader) Next() (sseEvent, error) {
	var eventType, data string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			eventType = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			data = after
		} else if line == "" && eventType != "" {
			return sseEvent{Type: eventType, Data: data}, nil
		}
	}
	if err := r.scanner.Err(); err != nil {
		return sseEvent{}, err
	}
	return sseEvent{}, io.EOF
}

func (r *sseReader) Close() {
	r.body.Close()
}
