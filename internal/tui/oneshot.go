package tui

import (
	"fmt"

	"github.com/justinpbarnett/virgil/internal/envelope"
)

func RunOneShot(signal string, serverAddr string) error {
	env, err := postSignal(serverAddr, signal)
	if err != nil {
		return err
	}

	if env.Error != nil {
		return fmt.Errorf("error (%s): %s", env.Error.Severity, env.Error.Message)
	}

	output := envelope.ContentToText(env.Content, env.ContentType)
	fmt.Println(output)
	return nil
}
