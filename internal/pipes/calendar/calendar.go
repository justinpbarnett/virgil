package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Time     string `json:"time"`
}

type CalendarClient interface {
	GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error)
}

func NewHandler(client CalendarClient, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		out := envelope.New("calendar", "list")
		out.Args = flags

		if client == nil {
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.FatalError("no calendar client configured — see SETUP.md for Google Calendar API setup")
			return out
		}

		rangeFlag := flags["range"]
		if rangeFlag == "" {
			rangeFlag = flags["modifier"]
			if rangeFlag == "" {
				rangeFlag = "today"
			}
		}

		timeMin, timeMax := resolveRange(rangeFlag)

		calendarID := flags["calendar"]
		if calendarID == "" {
			calendarID = "primary"
		}

		logger.Debug("fetching events", "range", rangeFlag, "calendar", calendarID)
		events, err := client.GetEvents(context.Background(), calendarID, timeMin, timeMax)
		if err != nil {
			logger.Error("calendar API error", "error", err)
			out.Duration = time.Since(out.Timestamp)
			out.Error = envelope.ClassifyError("calendar API", err)
			return out
		}

		if rangeFlag == "next" && len(events) > 1 {
			events = events[:1]
		}

		logger.Info("fetched", "count", len(events))
		out.Content = events
		out.ContentType = envelope.ContentList
		out.Duration = time.Since(out.Timestamp)
		return out
	}
}

func resolveRange(r string) (time.Time, time.Time) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	switch r {
	case "next":
		// Search from now through the end of the week so we find the next upcoming event.
		return now, today.Add(7 * 24 * time.Hour)
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

func formatEventTime(start, end string) string {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		// All-day event: date-only "2006-01-02"
		t, err := time.Parse("2006-01-02", start)
		if err != nil {
			return start
		}
		return t.Format("Mon, Jan 2")
	}

	if end == "" {
		return startTime.Format("Mon, Jan 2, 3:04 PM")
	}

	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return startTime.Format("Mon, Jan 2, 3:04 PM")
	}

	if startTime.Format("2006-01-02") == endTime.Format("2006-01-02") {
		if (startTime.Hour() < 12) == (endTime.Hour() < 12) {
			return startTime.Format("Mon, Jan 2, 3:04") + "–" + endTime.Format("3:04 PM")
		}
		return startTime.Format("Mon, Jan 2, 3:04 PM") + "–" + endTime.Format("3:04 PM")
	}

	return startTime.Format("Mon, Jan 2, 3:04 PM") + " – " + endTime.Format("Mon, Jan 2, 3:04 PM")
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
			Time:     formatEventTime(start, end),
		}
	}

	return events, nil
}
