package tui

import (
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
	"github.com/justinpbarnett/virgil/internal/sse"
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

func openSSEStream(ctx context.Context, serverAddr, text, model string) (*sse.Reader, error) {
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

	return sse.NewReader(resp.Body, 1024*1024), nil // 1 MB max line for large envelopes
}

type voiceStatusMsg struct {
	Recording bool
	Mode      string
	Model     string
	reader    *sse.Reader
}

type voiceModeExpiredMsg struct {
	generation int
}

type voiceReconnectMsg struct{}

// reconnectDelay is the standard delay before retrying a failed SSE connection.
const reconnectDelay = 5 * time.Second

func connectVoiceSSE(serverAddr, endpoint string, onConnect func(*sse.Reader) tea.Msg, onFail tea.Msg) tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("http://%s/voice/%s", serverAddr, endpoint)
		resp, err := streamClient.Get(url)
		if err != nil {
			time.Sleep(reconnectDelay)
			return onFail
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), envelope.SSEContentType) {
			resp.Body.Close()
			time.Sleep(reconnectDelay)
			return onFail
		}
		return onConnect(sse.NewReader(resp.Body, 64*1024))
	}
}

func connectVoiceStatus(serverAddr string) tea.Cmd {
	return connectVoiceSSE(serverAddr, "status", readNextVoiceEvent, voiceReconnectMsg{})
}

func readNextVoiceEvent(reader *sse.Reader) tea.Msg {
	for {
		event, err := reader.Next()
		if err != nil {
			reader.Close()
			time.Sleep(reconnectDelay)
			return voiceReconnectMsg{}
		}
		if event.Type == envelope.SSEEventVoiceStatus {
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

func readNextVoiceEventCmd(reader *sse.Reader) tea.Cmd {
	return func() tea.Msg {
		return readNextVoiceEvent(reader)
	}
}

// voiceInputMsg carries a transcript from the daemon to the TUI.
type voiceInputMsg struct {
	Text   string
	reader *sse.Reader
}

type voiceInputReconnectMsg struct{}

func connectVoiceInput(serverAddr string) tea.Cmd {
	return connectVoiceSSE(serverAddr, "input", readNextVoiceInput, voiceInputReconnectMsg{})
}

func readNextVoiceInput(reader *sse.Reader) tea.Msg {
	for {
		event, err := reader.Next()
		if err != nil {
			reader.Close()
			time.Sleep(reconnectDelay)
			return voiceInputReconnectMsg{}
		}
		if event.Type == envelope.SSEEventVoiceInput {
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

func readNextVoiceInputCmd(reader *sse.Reader) tea.Cmd {
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
