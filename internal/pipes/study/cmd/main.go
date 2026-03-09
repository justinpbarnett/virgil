package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/justinpbarnett/virgil/internal/pipehost"
	"github.com/justinpbarnett/virgil/internal/pipes/study"
	"github.com/justinpbarnett/virgil/internal/store"
	"github.com/justinpbarnett/virgil/internal/webscrape"
)

func main() {
	logger := pipehost.NewPipeLogger("study")

	// Provider is optional — AI compression (Tier 3) works without it
	provider, providerErr := pipehost.BuildProviderFromEnvWithLogger(logger)
	if providerErr != nil {
		logger.Warn("AI provider unavailable, Tier 3 compression disabled", "error", providerErr)
	}

	pc, err := pipehost.LoadPipeConfig()
	if err != nil {
		pipehost.Fatal("study", err.Error())
	}

	// Open memory store (optional — only needed for memory source)
	var s *store.Store
	dbPath := os.Getenv(pipehost.EnvDBPath)
	if dbPath != "" {
		s, err = store.Open(dbPath)
		if err != nil {
			logger.Warn("memory store unavailable", "error", err)
		} else {
			defer s.Close()
		}
	}

	// Determine workspace directory: prefer server's CWD over subprocess CWD
	workDir := os.Getenv(pipehost.EnvWorkDir)
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			pipehost.Fatal("study", fmt.Sprintf("cannot determine working directory: %v", err))
		}
	}

	// Build HTTP client and web fetcher for the web source
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	fetcher := webscrape.New(httpClient, logger)
	defer fetcher.Close()

	if key := os.Getenv("VIRGIL_BRIGHTDATA_KEY"); key != "" {
		fetcher.SetBrightDataKey(key)
	}

	// SearXNG searcher is optional — enabled only when VIRGIL_SEARXNG_URL is set
	var searcher webscrape.Searcher
	if searxngURL := os.Getenv("VIRGIL_SEARXNG_URL"); searxngURL != "" {
		searcher = webscrape.NewSearXNGSearcher(searxngURL, httpClient, logger)
		logger.Info("web search configured", "searxng_url", searxngURL)
	}

	handler := study.NewHandler(study.Config{
		Provider:   provider,
		Store:      s,
		WorkDir:    workDir,
		Fetcher:    fetcher,
		Searcher:   searcher,
		PipeConfig: pc,
		Logger:     logger,
	})

	logger.Info("initialized")
	pipehost.Run(handler, nil) // no streaming — study returns a complete result
}
