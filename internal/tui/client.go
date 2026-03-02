package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func postSignal(serverAddr, text string) (envelope.Envelope, error) {
	body, _ := json.Marshal(map[string]string{"text": text})

	resp, err := http.Post(
		fmt.Sprintf("http://%s/signal", serverAddr),
		"application/json",
		bytes.NewReader(body),
	)
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
