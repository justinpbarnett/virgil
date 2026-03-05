package calendar

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/justinpbarnett/virgil/internal/googleauth"
	"github.com/justinpbarnett/virgil/internal/pipe"
	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
	googlecalendar "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type Event struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Time        string `json:"time"`
}

type CalendarClient interface {
	GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error)
	CreateEvent(ctx context.Context, calendarID, title string, start, end time.Time, location, description string) (*Event, error)
	UpdateEvent(ctx context.Context, calendarID, eventID, title string, start, end time.Time, location, description string) (*Event, error)
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
}

func NewHandler(client CalendarClient, logger *slog.Logger) pipe.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(input envelope.Envelope, flags map[string]string) envelope.Envelope {
		action := flags["action"]
		if action == "" {
			action = "list"
		}

		if client == nil {
			out := envelope.New("calendar", action)
			out.Args = flags
			out.Error = envelope.FatalError("no calendar client configured — see SETUP.md for Google Calendar API setup")
			out.Duration = time.Since(out.Timestamp)
			return out
		}

		if flags["calendar"] == "" {
			flags["calendar"] = "primary"
		}

		switch action {
		case "list":
			return handleList(client, input, flags, logger)
		case "create":
			return handleCreate(client, input, flags, logger)
		case "update":
			return handleUpdate(client, input, flags, logger)
		case "delete":
			return handleDelete(client, input, flags, logger)
		default:
			out := envelope.New("calendar", action)
			out.Args = flags
			out.Error = envelope.FatalError(fmt.Sprintf("unknown action: %s", action))
			out.Duration = time.Since(out.Timestamp)
			return out
		}
	}
}

func handleList(client CalendarClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("calendar", "list")
	out.Args = flags

	rangeFlag := flags["range"]
	if rangeFlag == "" {
		rangeFlag = flags["modifier"]
		if rangeFlag == "" {
			rangeFlag = "today"
		}
	}

	timeMin, timeMax := resolveRange(rangeFlag)
	calendarID := flags["calendar"]

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

func handleCreate(client CalendarClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("calendar", "create")
	out.Args = flags

	title := flags["title"]
	if title == "" {
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.FatalError("title is required for create action")
		return out
	}

	startStr := flags["start"]
	if startStr == "" {
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.FatalError("start is required for create action")
		return out
	}

	start, err := parseEventTime(startStr)
	if err != nil {
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.FatalError(fmt.Sprintf("invalid start time: %v", err))
		return out
	}

	end := start.Add(time.Hour)
	if endStr := flags["end"]; endStr != "" {
		if t, err := parseEventTime(endStr); err == nil {
			end = t
		}
	}

	calendarID := flags["calendar"]
	location := flags["location"]
	description := flags["description"]

	logger.Debug("creating event", "title", title, "start", start)
	event, err := client.CreateEvent(context.Background(), calendarID, title, start, end, location, description)
	if err != nil {
		logger.Error("create event failed", "error", err)
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.ClassifyError("calendar API", err)
		return out
	}

	logger.Info("created event", "id", event.ID)
	out.Content = event
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleUpdate(client CalendarClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("calendar", "update")
	out.Args = flags

	eventID := flags["event_id"]
	if eventID == "" {
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.FatalError("event_id is required for update action")
		return out
	}

	calendarID := flags["calendar"]
	title := flags["title"]
	location := flags["location"]
	description := flags["description"]

	var start, end time.Time
	if startStr := flags["start"]; startStr != "" {
		if t, err := parseEventTime(startStr); err == nil {
			start = t
		}
	}
	if endStr := flags["end"]; endStr != "" {
		if t, err := parseEventTime(endStr); err == nil {
			end = t
		}
	}

	logger.Debug("updating event", "event_id", eventID)
	event, err := client.UpdateEvent(context.Background(), calendarID, eventID, title, start, end, location, description)
	if err != nil {
		logger.Error("update event failed", "error", err)
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.ClassifyError("calendar API", err)
		return out
	}

	logger.Info("updated event", "id", event.ID)
	out.Content = event
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

func handleDelete(client CalendarClient, _ envelope.Envelope, flags map[string]string, logger *slog.Logger) envelope.Envelope {
	out := envelope.New("calendar", "delete")
	out.Args = flags

	eventID := flags["event_id"]
	if eventID == "" {
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.FatalError("event_id is required for delete action")
		return out
	}

	calendarID := flags["calendar"]

	logger.Debug("deleting event", "event_id", eventID)
	err := client.DeleteEvent(context.Background(), calendarID, eventID)
	if err != nil {
		logger.Error("delete event failed", "error", err)
		out.Duration = time.Since(out.Timestamp)
		out.Error = envelope.ClassifyError("calendar API", err)
		return out
	}

	logger.Info("deleted event", "id", eventID)
	out.Content = map[string]string{
		"status":   "deleted",
		"event_id": eventID,
	}
	out.ContentType = envelope.ContentStructured
	out.Duration = time.Since(out.Timestamp)
	return out
}

// whenParser is shared across calls.
var whenParser = func() *when.Parser {
	w := when.New(nil)
	w.Add(en.All...)
	w.Add(common.All...)
	return w
}()

func resolveRange(r string) (time.Time, time.Time) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// "next" is a special modifier meaning "next upcoming event" — not a date expression.
	if r == "next" {
		return now, today.Add(7 * 24 * time.Hour)
	}

	// Normalise "this-week" → "this week" for the parser.
	expr := r
	if expr == "this-week" {
		expr = "this week"
	}

	// Try natural language parsing (handles "tomorrow", "next friday", "in 3 days", etc.)
	if result, err := whenParser.Parse(expr, now); err == nil && result != nil {
		t := result.Time
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		// "this week" expands to a 7-day window; single-day expressions get a 1-day window.
		if r == "this-week" {
			return start, start.Add(7 * 24 * time.Hour)
		}
		return start, start.Add(24 * time.Hour)
	}

	// Default to today.
	return today, today.Add(24 * time.Hour)
}

func parseEventTime(s string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try natural language
	now := time.Now()
	if result, err := whenParser.Parse(s, now); err == nil && result != nil {
		return result.Time, nil
	}
	return time.Time{}, fmt.Errorf("could not parse time: %q", s)
}

// GoogleCalendarClient implements CalendarClient using Google Calendar API
type GoogleCalendarClient struct {
	svc *googlecalendar.Service
}

func NewGoogleClient(configDir string) (*GoogleCalendarClient, error) {
	httpClient, err := googleauth.NewHTTPClient(configDir, "https://www.googleapis.com/auth/calendar")
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	svc, err := googlecalendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating calendar service: %w", err)
	}

	return &GoogleCalendarClient{svc: svc}, nil
}

func (g *GoogleCalendarClient) GetEvents(ctx context.Context, calendarID string, timeMin, timeMax time.Time) ([]Event, error) {
	result, err := g.svc.Events.List(calendarID).
		TimeMin(timeMin.Format(time.RFC3339)).
		TimeMax(timeMax.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	events := make([]Event, len(result.Items))
	for i, item := range result.Items {
		events[i] = mapEvent(item)
	}
	return events, nil
}

func (g *GoogleCalendarClient) CreateEvent(ctx context.Context, calendarID, title string, start, end time.Time, location, description string) (*Event, error) {
	event := &googlecalendar.Event{
		Summary:     title,
		Location:    location,
		Description: description,
		Start:       &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:         &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)},
	}

	created, err := g.svc.Events.Insert(calendarID, event).Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	e := mapEvent(created)
	return &e, nil
}

func (g *GoogleCalendarClient) UpdateEvent(ctx context.Context, calendarID, eventID, title string, start, end time.Time, location, description string) (*Event, error) {
	// Fetch existing event to preserve unmodified fields
	existing, err := g.svc.Events.Get(calendarID, eventID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("fetching event %s: %w", eventID, err)
	}

	if title != "" {
		existing.Summary = title
	}
	if location != "" {
		existing.Location = location
	}
	if description != "" {
		existing.Description = description
	}
	if !start.IsZero() {
		existing.Start = &googlecalendar.EventDateTime{DateTime: start.Format(time.RFC3339)}
	}
	if !end.IsZero() {
		existing.End = &googlecalendar.EventDateTime{DateTime: end.Format(time.RFC3339)}
	}

	updated, err := g.svc.Events.Update(calendarID, eventID, existing).Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	e := mapEvent(updated)
	return &e, nil
}

func (g *GoogleCalendarClient) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	return g.svc.Events.Delete(calendarID, eventID).Context(ctx).Do()
}

func mapEvent(item *googlecalendar.Event) Event {
	start := item.Start.DateTime
	if start == "" {
		start = item.Start.Date
	}
	end := item.End.DateTime
	if end == "" {
		end = item.End.Date
	}
	return Event{
		ID:          item.Id,
		Title:       item.Summary,
		Start:       start,
		End:         end,
		Location:    item.Location,
		Description: item.Description,
		Time:        formatEventTime(start, end),
	}
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
