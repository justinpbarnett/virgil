package tui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func IsPiped() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func RunPipe(signal string, serverAddr string) error {
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}

	piped := strings.TrimSpace(string(stdin))

	var combined string
	switch {
	case piped != "" && signal != "":
		combined = signal + "\n" + piped
	case piped != "":
		combined = piped
	case signal != "":
		combined = signal
	default:
		return fmt.Errorf("no input: expected stdin or signal argument")
	}

	env, err := postSignal(serverAddr, combined)
	if err != nil {
		return err
	}

	if env.Error != nil {
		return fmt.Errorf("error (%s): %s", env.Error.Severity, env.Error.Message)
	}

	fmt.Println(envelope.ContentToText(env.Content, env.ContentType))
	return nil
}
