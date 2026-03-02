package main

import (
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/calendar"
)

func main() {
	logger := pipehost.NewPipeLogger("calendar")

	userDir := os.Getenv(pipehost.EnvUserDir)

	client, err := calendar.NewGoogleClient(userDir)
	if err != nil {
		logger.Warn("calendar client unavailable, continuing without", "error", err)
		pipehost.Run(calendar.NewHandler(nil, logger), nil)
		return
	}

	logger.Info("initialized")
	pipehost.Run(calendar.NewHandler(client, logger), nil)
}
