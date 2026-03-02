package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func RunOneShot(signal string, serverAddr string) error {
	ctx := context.Background()
	reader, err := openSSEStream(ctx, serverAddr, signal)
	if err != nil {
		// Fall back to non-streaming
		return runOneShotSync(signal, serverAddr)
	}
	defer reader.Close()

	for {
		event, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("stream error: %w", err)
		}

		switch event.Type {
		case envelope.SSEEventChunk:
			var chunk struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				return fmt.Errorf("invalid chunk: %w", err)
			}
			fmt.Print(chunk.Text)

		case envelope.SSEEventDone:
			var env envelope.Envelope
			if err := json.Unmarshal([]byte(event.Data), &env); err != nil {
				return fmt.Errorf("invalid done event: %w", err)
			}
			if env.Error != nil {
				return fmt.Errorf("error (%s): %s", env.Error.Severity, env.Error.Message)
			}
			// If no chunks were streamed, print the final content
			content := envelope.ContentToText(env.Content, env.ContentType)
			if content != "" {
				fmt.Println(content)
			} else {
				fmt.Println() // newline after streamed chunks
			}
			return nil
		}
	}
}

func runOneShotSync(signal string, serverAddr string) error {
	env, err := postSignal(serverAddr, signal)
	if err != nil {
		return err
	}

	if env.Error != nil {
		return fmt.Errorf("error (%s): %s", env.Error.Severity, env.Error.Message)
	}

	output := envelope.ContentToText(env.Content, env.ContentType)
	fmt.Fprintln(os.Stdout, output)
	return nil
}

