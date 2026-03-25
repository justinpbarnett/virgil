package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxResponseBytes = 10 << 20 // 10 MB

func intParam(params map[string]any, key string, defaultVal int) int {
	if v, ok := params[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

func parseDate(s string) time.Time {
	now := time.Now()
	switch strings.ToLower(s) {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "yesterday":
		y := now.AddDate(0, 0, -1)
		return time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, now.Location())
	case "tomorrow":
		t := now.AddDate(0, 0, 1)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	default:
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
		return time.Time{}
	}
}

func parseDateTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04", s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04", s); err == nil {
		return t
	}
	return parseDate(s)
}

// requireAccount returns an error if a specific account name was requested but
// is not in the client map. Empty account means "all accounts" and always passes.
func requireAccount[T any](clients map[string]T, account string) error {
	if account == "" {
		return nil
	}
	if _, ok := clients[account]; ok {
		return nil
	}
	names := make([]string, 0, len(clients))
	for k := range clients {
		names = append(names, k)
	}
	sort.Strings(names)
	return fmt.Errorf("account %q not configured (available: %s)", account, strings.Join(names, ", "))
}

func filterClients[T any](clients map[string]T, account string) map[string]T {
	if account == "" {
		return clients
	}
	if c, ok := clients[account]; ok {
		return map[string]T{account: c}
	}
	return nil
}

func httpJSON(req *http.Request) (map[string]any, error) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 204 No Content and similar success responses with no body
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusResetContent || len(body) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}
