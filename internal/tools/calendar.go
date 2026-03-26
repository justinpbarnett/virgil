package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/config"
	vgoogle "github.com/justinpbarnett/virgil/internal/google"
	"github.com/justinpbarnett/virgil/internal/trust"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// RegisterCalendarTools registers calendar_check, calendar_create, and calendar_delete.
func RegisterCalendarTools(reg *Registry, cfg *config.Config, ts *trust.Store) {
	clients := make(map[string]*calendar.Service)
	var initErrs []string

	for name, acct := range cfg.Channels.Calendar.Accounts {
		if acct.CredentialsPath == "" {
			continue
		}
		httpClient, err := vgoogle.NewHTTPClient(acct.CredentialsPath)
		if err != nil {
			slog.Warn("skip calendar account", "account", name, "err", err)
			initErrs = append(initErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		svc, err := calendar.NewService(context.Background(), option.WithHTTPClient(httpClient))
		if err != nil {
			slog.Warn("skip calendar account", "account", name, "err", err)
			initErrs = append(initErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		clients[name] = svc
	}

	reg.Register(&internal.Tool{
		Name:        "calendar_check",
		Description: "Check calendar events for a date range. Defaults to today.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account": map[string]any{
					"type":        "string",
					"description": "Calendar account. Defaults to all.",
				},
				"start": map[string]any{
					"type":        "string",
					"description": "Start date/time (today, tomorrow, 2026-03-25)",
				},
				"end": map[string]any{
					"type":        "string",
					"description": "End date/time",
				},
				"days": map[string]any{
					"type":        "integer",
					"description": "Number of days from start. Default 1.",
				},
			},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			if len(clients) == 0 {
				if len(initErrs) > 0 {
					return &internal.ToolResult{Error: fmt.Sprintf("no calendar accounts available (init errors: %s)", strings.Join(initErrs, "; "))}, nil
				}
				return &internal.ToolResult{Error: "no calendar accounts configured"}, nil
			}

			account, _ := params["account"].(string)
			if err := requireAccount(clients, account); err != nil {
				return &internal.ToolResult{Error: err.Error()}, nil
			}
			startStr, _ := params["start"].(string)
			endStr, _ := params["end"].(string)
			days := intParam(params, "days", 1)

			start := parseDate(startStr)
			if start.IsZero() {
				now := time.Now()
				start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			}
			var end time.Time
			if endStr != "" {
				end = parseDate(endStr)
			}
			if end.IsZero() {
				end = start.Add(time.Duration(days) * 24 * time.Hour)
			}

			var results []map[string]any
			var errs []string
			for acctName, svc := range filterClients(clients, account) {
				events, err := svc.Events.List("primary").
					TimeMin(start.Format(time.RFC3339)).
					TimeMax(end.Format(time.RFC3339)).
					SingleEvents(true).
					OrderBy("startTime").
					Do()
				if err != nil {
					slog.Warn("calendar_check error", "account", acctName, "err", err)
					errs = append(errs, fmt.Sprintf("%s: %s", acctName, err))
					continue
				}
				for _, e := range events.Items {
					results = append(results, calFormatEvent(e, acctName))
				}
			}
			if len(results) == 0 && len(errs) > 0 {
				return &internal.ToolResult{Error: fmt.Sprintf("all accounts failed: %s", strings.Join(errs, "; "))}, nil
			}
			if results == nil {
				results = []map[string]any{}
			}
			return partialResult("events", results, errs), nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "calendar_create",
		Description: "Create a calendar event.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"account":     map[string]any{"type": "string"},
				"title":       map[string]any{"type": "string"},
				"start":       map[string]any{"type": "string"},
				"end":         map[string]any{"type": "string"},
				"location":    map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"attendees": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"required": []string{"account", "title", "start", "end"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			account, _ := params["account"].(string)
			title, _ := params["title"].(string)
			startStr, _ := params["start"].(string)
			endStr, _ := params["end"].(string)
			location, _ := params["location"].(string)
			description, _ := params["description"].(string)

			if account == "" || title == "" || startStr == "" || endStr == "" {
				return &internal.ToolResult{Error: "account, title, start, and end are required"}, nil
			}

			if blocked := checkTrust(ctx, ts, "calendar_create", account, "*"); blocked != nil {
				return blocked, nil
			}

			svc, ok := clients[account]
			if !ok {
				return &internal.ToolResult{Error: fmt.Sprintf("calendar account %q not configured", account)}, nil
			}

			startTime := parseDateTime(startStr)
			if startTime.IsZero() {
				return &internal.ToolResult{Error: fmt.Sprintf("could not parse start time %q", startStr)}, nil
			}
			endTime := parseDateTime(endStr)
			if endTime.IsZero() {
				return &internal.ToolResult{Error: fmt.Sprintf("could not parse end time %q", endStr)}, nil
			}

			event := &calendar.Event{
				Summary:     title,
				Location:    location,
				Description: description,
				Start:       &calendar.EventDateTime{DateTime: startTime.Format(time.RFC3339)},
				End:         &calendar.EventDateTime{DateTime: endTime.Format(time.RFC3339)},
			}

			if atts, ok := params["attendees"].([]any); ok {
				for _, a := range atts {
					if email, ok := a.(string); ok {
						event.Attendees = append(event.Attendees, &calendar.EventAttendee{Email: email})
					}
				}
			}

			created, err := svc.Events.Insert("primary", event).Do()
			if err != nil {
				return nil, fmt.Errorf("calendar_create: %w", err)
			}

			return &internal.ToolResult{Data: calFormatEvent(created, account)}, nil
		},
	})

	reg.Register(&internal.Tool{
		Name:        "calendar_delete",
		Description: "Delete a calendar event.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"event_id": map[string]any{"type": "string"},
				"account":  map[string]any{"type": "string"},
			},
			"required": []string{"event_id", "account"},
		},
		Execute: func(ctx context.Context, params map[string]any) (*internal.ToolResult, error) {
			eventID, _ := params["event_id"].(string)
			account, _ := params["account"].(string)

			if eventID == "" || account == "" {
				return &internal.ToolResult{Error: "event_id and account are required"}, nil
			}

			if blocked := checkTrust(ctx, ts, "calendar_delete", account, "*"); blocked != nil {
				return blocked, nil
			}

			svc, ok := clients[account]
			if !ok {
				return &internal.ToolResult{Error: fmt.Sprintf("calendar account %q not configured", account)}, nil
			}

			if err := svc.Events.Delete("primary", eventID).Do(); err != nil {
				return nil, fmt.Errorf("calendar_delete: %w", err)
			}

			return &internal.ToolResult{Data: map[string]any{
				"event_id": eventID,
				"account":  account,
				"status":   "deleted",
			}}, nil
		},
	})
}

func calFormatEvent(e *calendar.Event, account string) map[string]any {
	start, end := "", ""
	if e.Start != nil {
		start = e.Start.DateTime
		if start == "" {
			start = e.Start.Date
		}
	}
	if e.End != nil {
		end = e.End.DateTime
		if end == "" {
			end = e.End.Date
		}
	}

	attendees := make([]string, 0, len(e.Attendees))
	for _, a := range e.Attendees {
		attendees = append(attendees, a.Email)
	}

	return map[string]any{
		"id":          e.Id,
		"account":     account,
		"title":       e.Summary,
		"start":       start,
		"end":         end,
		"location":    e.Location,
		"description": e.Description,
		"attendees":   attendees,
		"link":        e.HtmlLink,
	}
}
