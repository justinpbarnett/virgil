package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/mail"
)

func main() {
	logger := pipehost.NewPipeLogger("mail")

	userDir := os.Getenv(pipehost.EnvUserDir)

	client, err := mail.NewGmailClient(userDir)
	if err != nil {
		logger.Warn("mail client unavailable", "error", err)
		pipehost.Run(mail.NewHandler(nil, logger), nil)
		return
	}

	logger.Info("initialized")
	pipehost.Run(mail.NewHandler(client, logger), nil)
}
