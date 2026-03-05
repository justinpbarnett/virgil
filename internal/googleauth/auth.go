package googleauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// NewHTTPClient loads credentials and token from configDir and returns an
// authenticated HTTP client with automatic token refresh.
func NewHTTPClient(configDir string, scopes ...string) (*http.Client, error) {
	credPath := filepath.Join(configDir, "google-credentials.json")
	credData, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w (see SETUP.md)", err)
	}

	config, err := google.ConfigFromJSON(credData, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	tokenPath := filepath.Join(configDir, "google-token.json")
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("reading token: %w (run token flow first, see SETUP.md)", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	ctx := context.Background()
	tokenSource := oauth2.ReuseTokenSource(&token, config.TokenSource(ctx, &token))
	return oauth2.NewClient(ctx, tokenSource), nil
}
