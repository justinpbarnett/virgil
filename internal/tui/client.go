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

	tea "github.com/charmbracelet/bubbletea"
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

func signalBody(text, model string) io.Reader {
	data := map[string]any{"text": text}
	if model != "" {
		data["model"] = model
	}
	body, _ := json.Marshal(data)
	return bytes.NewReader(body)
}

func postSignal(serverAddr, text string) (envelope.Envelope, error) {
	resp, err := signalClient.Post(signalURL(serverAddr), "application/json", signalBody(text, ""))
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

func openSSEStream(ctx context.Context, serverAddr, text, model string) (*sseReader, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", signalURL(serverAddr), signalBody(text, model))
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

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, envelope.SSEContentType) {
		resp.Body.Close()
		return nil, fmt.Errorf("server does not support streaming (got %s)", ct)
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

type voiceStatusMsg struct {
	Recording bool
	Mode      string
	Model     string
	reader    *sseReader
}

type voiceModeExpiredMsg struct {
	generation int
}

type voiceReconnectMsg struct{}

func connectVoiceStatus(serverAddr string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://%s/voice/status", serverAddr)
		resp, err := streamClient.Get(url)
		if err != nil {
			time.Sleep(5 * time.Second)
			return voiceReconnectMsg{}
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, envelope.SSEContentType) {
			resp.Body.Close()
			time.Sleep(5 * time.Second)
			return voiceReconnectMsg{}
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024), 64*1024)
		reader := &sseReader{scanner: scanner, body: resp.Body}
		return readNextVoiceEvent(reader)
	}
}

func readNextVoiceEvent(reader *sseReader) tea.Msg {
	for {
		event, err := reader.Next()
		if err != nil {
			reader.Close()
			time.Sleep(5 * time.Second)
			return voiceReconnectMsg{}
		}
		if event.Type == "voice_status" {
			var vs struct {
				Recording bool   `json:"recording"`
				Mode      string `json:"mode"`
				Model     string `json:"model"`
			}
			if err := json.Unmarshal([]byte(event.Data), &vs); err != nil {
				continue
			}
			return voiceStatusMsg{
				Recording: vs.Recording,
				Mode:      vs.Mode,
				Model:     vs.Model,
				reader:    reader,
			}
		}
	}
}

func readNextVoiceEventCmd(reader *sseReader) tea.Cmd {
	return func() tea.Msg {
		return readNextVoiceEvent(reader)
	}
}

// voiceInputMsg carries a transcript from the daemon to the TUI.
type voiceInputMsg struct {
	Text   string
	reader *sseReader
}

type voiceInputReconnectMsg struct{}

func connectVoiceInput(serverAddr string) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://%s/voice/input", serverAddr)
		resp, err := streamClient.Get(url)
		if err != nil {
			time.Sleep(5 * time.Second)
			return voiceInputReconnectMsg{}
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Body.Close()
			time.Sleep(5 * time.Second)
			return voiceInputReconnectMsg{}
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		reader := &sseReader{scanner: scanner, body: resp.Body}
		return readNextVoiceInput(reader)
	}
}

func readNextVoiceInput(reader *sseReader) tea.Msg {
	for {
		event, err := reader.Next()
		if err != nil {
			reader.Close()
			time.Sleep(5 * time.Second)
			return voiceInputReconnectMsg{}
		}
		if event.Type == "voice_input" {
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Data), &payload); err != nil || payload.Text == "" {
				continue
			}
			return voiceInputMsg{Text: payload.Text, reader: reader}
		}
	}
}

func readNextVoiceInputCmd(reader *sseReader) tea.Cmd {
	return func() tea.Msg {
		return readNextVoiceInput(reader)
	}
}

func postVoice(serverAddr, endpoint string, payload any) tea.Cmd {
	return func() tea.Msg {
		body, _ := json.Marshal(payload)
		resp, err := signalClient.Post(
			fmt.Sprintf("http://%s/voice/%s", serverAddr, endpoint),
			"application/json",
			bytes.NewReader(body),
		)
		if err != nil {
			return nil
		}
		resp.Body.Close()
		return nil
	}
}

func postVoiceSpeak(serverAddr, text, priority string) tea.Cmd {
	return postVoice(serverAddr, "speak", map[string]string{"text": text, "priority": priority})
}

func postVoiceStop(serverAddr string) tea.Cmd {
	return postVoice(serverAddr, "stop", struct{}{})
}

func postVoiceCycle(serverAddr string) tea.Cmd {
	return postVoice(serverAddr, "cycle", struct{}{})
}
