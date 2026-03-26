package google

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
// or from GOOGLE_CLIENT_ID_<ACCOUNT> / GOOGLE_CLIENT_SECRET_<ACCOUNT> env vars
// (where account is derived from the token filename, e.g. google-token-passion.json -> PASSION),
// falling back to GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET.
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

	account := accountFromTokenPath(tokenPath)
	clientID, clientSecret, err := LoadClientCredentials(filepath.Dir(tokenPath), account)
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

// accountFromTokenPath derives an account name from a token filename.
// e.g. "/data/google-token-passion.json" -> "passion"
func accountFromTokenPath(tokenPath string) string {
	base := filepath.Base(tokenPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimPrefix(base, "google-token-")
	return base
}

// LoadClientCredentials loads OAuth2 client ID and secret from credentials.json
// in the given directory, or from per-account env vars
// (GOOGLE_CLIENT_ID_<ACCOUNT> / GOOGLE_CLIENT_SECRET_<ACCOUNT>),
// falling back to GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET.
func LoadClientCredentials(dir, account string) (string, string, error) {
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

	if account != "" {
		suffix := strings.ToUpper(account)
		clientID := os.Getenv("GOOGLE_CLIENT_ID_" + suffix)
		clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET_" + suffix)
		if clientID != "" && clientSecret != "" {
			return clientID, clientSecret, nil
		}
	}

	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID != "" && clientSecret != "" {
		return clientID, clientSecret, nil
	}

	return "", "", fmt.Errorf("Google client credentials not found: place credentials.json in %s or set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET", dir)
}
