package main

import (
	"fmt"
	"os"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/calendar"
)

func main() {
	userDir := os.Getenv(pipehost.EnvUserDir)

	client, err := calendar.NewGoogleClient(userDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "calendar: %v (continuing without client)\n", err)
		pipehost.Run(calendar.NewHandler(nil), nil)
		return
	}
	pipehost.Run(calendar.NewHandler(client), nil)
}
