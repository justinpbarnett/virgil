package voice

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const whisperURL = "https://api.openai.com/v1/audio/transcriptions"

// STTClient transcribes audio via OpenAI's Whisper API.
type STTClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewSTTClient creates a Whisper STT client.
func NewSTTClient(apiKey string) *STTClient {
	return &STTClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Transcribe sends the audio file at audioPath to Whisper and returns the transcript.
// It removes the audio file after transcription regardless of success or failure.
func (s *STTClient) Transcribe(ctx context.Context, audioPath string) (string, error) {
	defer os.Remove(audioPath)

	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("opening audio file: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	part, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", fmt.Errorf("creating form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("copying audio data: %w", err)
	}
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("response_format", "text")
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", whisperURL, &body)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return strings.TrimSpace(string(data)), nil
	case http.StatusUnauthorized:
		return "", fmt.Errorf("invalid OpenAI API key (401)")
	case http.StatusRequestEntityTooLarge:
		return "", fmt.Errorf("audio file too large: Whisper has a 25MB limit (413)")
	default:
		return "", fmt.Errorf("Whisper API error (%d): %s", resp.StatusCode, string(data))
	}
}
