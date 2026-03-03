package main

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

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Join(home, ".config", "virgil")
	credPath := filepath.Join(configDir, "google-credentials.json")
	tokenPath := filepath.Join(configDir, "google-token.json")

	credData, err := os.ReadFile(credPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not read %s: %v\n", credPath, err)
		fmt.Fprintf(os.Stderr, "Download OAuth2 credentials from GCP Console first (see SETUP.md)\n")
		os.Exit(1)
	}

	config, err := google.ConfigFromJSON(credData,
		"https://www.googleapis.com/auth/calendar.readonly",
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/gmail.send",
		"https://www.googleapis.com/auth/gmail.modify",
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid credentials file: %v\n", err)
		os.Exit(1)
	}

	// Use a local redirect to capture the auth code automatically
	config.RedirectURL = "http://localhost:8089/callback"

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprintf(w, "Error: no authorization code received. Close this tab and try again.")
			return
		}
		codeCh <- code
		fmt.Fprintf(w, "Authorization successful! You can close this tab.")
	})

	srv := &http.Server{Addr: ":8089", Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	authURL := config.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Printf("Opening browser for Google authorization...\n\n")
	fmt.Printf("If the browser doesn't open, visit this URL:\n%s\n\n", authURL)

	// Try to open the browser
	openBrowser(authURL)

	fmt.Printf("Waiting for authorization...\n")

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	srv.Shutdown(context.Background())

	token, err := config.Exchange(context.Background(), code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: token exchange failed: %v\n", err)
		os.Exit(1)
	}

	tokenData, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(tokenPath, tokenData, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "error: could not write %s: %v\n", tokenPath, err)
		os.Exit(1)
	}

	fmt.Printf("Token saved to %s\n", tokenPath)
}
