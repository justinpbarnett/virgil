package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
	goauth "golang.org/x/oauth2/google"
)

// Scopes requested by all Google API tools.
var Scopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/drive.readonly",
}

// NewHTTPClient creates an auto-refreshing OAuth2 HTTP client from a token file.
// Client credentials are loaded from credentials.json next to the token file,
// or from GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET environment variables.
func NewHTTPClient(tokenPath string) (*http.Client, error) {
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read token file %s: %w", tokenPath, err)
	}

	var tok oauth2.Token
	if err := json.Unmarshal(tokenData, &tok); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if tok.RefreshToken == "" {
		return nil, fmt.Errorf("token file %s has no refresh_token", tokenPath)
	}

	clientID, clientSecret, err := LoadClientCredentials(filepath.Dir(tokenPath))
	if err != nil {
		return nil, err
	}

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     goauth.Endpoint,
		Scopes:       Scopes,
	}

	return cfg.Client(context.Background(), &tok), nil
}

// LoadClientCredentials loads OAuth2 client ID and secret from credentials.json
// in the given directory, or from GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET env vars.
func LoadClientCredentials(dir string) (string, string, error) {
	credPath := filepath.Join(dir, "credentials.json")
	if data, err := os.ReadFile(credPath); err == nil {
		var cred struct {
			Installed struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"installed"`
			Web struct {
				ClientID     string `json:"client_id"`
				ClientSecret string `json:"client_secret"`
			} `json:"web"`
		}
		if err := json.Unmarshal(data, &cred); err != nil {
			slog.Warn("parse credentials.json failed", "path", credPath, "err", err)
		} else {
			if cred.Installed.ClientID != "" {
				return cred.Installed.ClientID, cred.Installed.ClientSecret, nil
			}
			if cred.Web.ClientID != "" {
				return cred.Web.ClientID, cred.Web.ClientSecret, nil
			}
		}
	}

	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID != "" && clientSecret != "" {
		return clientID, clientSecret, nil
	}

	return "", "", fmt.Errorf("Google client credentials not found: place credentials.json in %s or set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET", dir)
}
