package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Event struct {
	Title    string `json:"title"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Location string `json:"location"`
}

type CalendarClient interface {
	GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error)
}

func NewHandler(client CalendarClient) pipe.Handler {
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("calendar", "list")
		out.Args = flags

		if client == nil {
			out.Error = envelope.FatalError("no calendar client configured — see SETUP.md for Google Calendar API setup")
			return out
		}

		rangeFlag := flags["range"]
		if rangeFlag == "" {
			rangeFlag = "today"
		}

		timeMin, timeMax := resolveRange(rangeFlag)

		calendarID := flags["calendar"]
		if calendarID == "" {
			calendarID = "primary"
		}

		events, err := client.GetEvents(context.Background(), calendarID, timeMin, timeMax)
		if err != nil {
			out.Error = envelope.FatalError(fmt.Sprintf("calendar API error: %v", err))
			return out
		}

		out.Content = events
		out.ContentType = envelope.ContentList
		return out
	}
}

func resolveRange(r string) (time.Time, time.Time) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch r {
	case "tomorrow":
		start := today.Add(24 * time.Hour)
		return start, start.Add(24 * time.Hour)
	case "this-week":
		weekday := int(today.Weekday())
		start := today.Add(-time.Duration(weekday) * 24 * time.Hour)
		return start, start.Add(7 * 24 * time.Hour)
	default: // "today"
		return today, today.Add(24 * time.Hour)
	}
}

// GoogleCalendarClient implements CalendarClient using Google Calendar API
type GoogleCalendarClient struct {
	httpClient *http.Client
}

func NewGoogleClient(configDir string) (*GoogleCalendarClient, error) {
	credPath := filepath.Join(configDir, "google-credentials.json")
	credData, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w (see SETUP.md)", err)
	}

	config, err := google.ConfigFromJSON(credData, "https://www.googleapis.com/auth/calendar.readonly")
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

	return &GoogleCalendarClient{
		httpClient: config.Client(context.Background(), &token),
	}, nil
}

func (g *GoogleCalendarClient) GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error) {
	u := &url.URL{
		Scheme: "https",
		Host:   "www.googleapis.com",
		Path:   fmt.Sprintf("/calendar/v3/calendars/%s/events", calendarID),
	}
	q := u.Query()
	q.Set("timeMin", timeMin.Format(time.RFC3339))
	q.Set("timeMax", timeMax.Format(time.RFC3339))
	q.Set("singleEvents", "true")
	q.Set("orderBy", "startTime")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calendar API returned %d", resp.StatusCode)
	}

	var result struct {
		Items []struct {
			Summary  string `json:"summary"`
			Location string `json:"location"`
			Start    struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"start"`
			End struct {
				DateTime string `json:"dateTime"`
				Date     string `json:"date"`
			} `json:"end"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	events := make([]Event, len(result.Items))
	for i, item := range result.Items {
		start := item.Start.DateTime
		if start == "" {
			start = item.Start.Date
		}
		end := item.End.DateTime
		if end == "" {
			end = item.End.Date
		}
		events[i] = Event{
			Title:    item.Summary,
			Start:    start,
			End:      end,
			Location: item.Location,
		}
	}

	return events, nil
}
