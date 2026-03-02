package main

import (
	"github.com/justinpbarnett/virgil/internal/bridge"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/chat"
)

func main() {
	provider, err := pipehost.BuildProviderFromEnv()
	if err != nil {
		pipehost.Fatal("chat", err.Error())
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("chat", err.Error())
	}

	systemPrompt := pc.Prompts.System

	pipehost.RunWithStreaming(provider, chat.NewHandler(provider, systemPrompt), func(sp bridge.StreamingProvider) pipe.StreamHandler {
		return chat.NewStreamHandler(sp, systemPrompt)
	})
}
