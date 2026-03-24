package observe

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/db"
)

// TimestampFormat is the ISO 8601 format used in SQLite and Go parsing.
// The SQL schema uses strftime('%Y-%m-%dT%H:%M:%fZ') which produces SS.SSS.
const TimestampFormat = time.RFC3339Nano

// EventLog writes structured events to the events table.
type EventLog struct {
	db *sql.DB
}

// NewEventLog creates an EventLog backed by the given database.
func NewEventLog(db *sql.DB) *EventLog {
	return &EventLog{db: db}
}

// Log writes an event to the database.
func (e *EventLog) Log(ev *internal.Event) error {
	_, err := e.db.Exec(`
		INSERT INTO events (component, action, input, output, duration_ms, error, trace_id, span_id, parent_span, model, tokens_in, tokens_out)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Component, ev.Action,
		db.NullStr(ev.Input), db.NullStr(ev.Output), ev.DurationMs,
		db.NullStr(ev.Error), db.NullStr(ev.TraceID), db.NullStr(ev.SpanID), db.NullStr(ev.ParentSpan),
		db.NullStr(ev.Model), ev.TokensIn, ev.TokensOut,
	)
	return err
}

// Query returns events matching the given filters.
func (e *EventLog) Query(traceID string, component string, limit int) ([]internal.Event, error) {
	q := "SELECT id, timestamp, component, action, input, output, duration_ms, error, trace_id, span_id, parent_span, model, tokens_in, tokens_out FROM events WHERE 1=1"
	var args []any

	if traceID != "" {
		q += " AND trace_id = ?"
		args = append(args, traceID)
	}
	if component != "" {
		q += " AND component = ?"
		args = append(args, component)
	}

	q += " ORDER BY id DESC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := e.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]internal.Event, 0)
	for rows.Next() {
		var ev internal.Event
		var ts string
		var errStr sql.NullString
		var input, output, trID, spID, pSpan, model sql.NullString
		var tokIn, tokOut sql.NullInt64
		var durMs sql.NullInt64

		if err := rows.Scan(&ev.ID, &ts, &ev.Component, &ev.Action,
			&input, &output, &durMs, &errStr,
			&trID, &spID, &pSpan, &model, &tokIn, &tokOut); err != nil {
			return nil, err
		}

		parsed, parseErr := time.Parse(TimestampFormat, ts)
		if parseErr != nil {
			return nil, fmt.Errorf("parse event timestamp %q (id=%d): %w", ts, ev.ID, parseErr)
		}
		ev.Timestamp = parsed
		ev.Input = input.String
		ev.Output = output.String
		ev.DurationMs = durMs.Int64
		ev.Error = errStr.String
		ev.TraceID = trID.String
		ev.SpanID = spID.String
		ev.ParentSpan = pSpan.String
		ev.Model = model.String
		ev.TokensIn = int(tokIn.Int64)
		ev.TokensOut = int(tokOut.Int64)

		events = append(events, ev)
	}
	return events, rows.Err()
}

// GenerateTraceID returns a random 16-byte hex string.
func GenerateTraceID() string {
	return randomHex(16)
}

// GenerateSpanID returns a random 8-byte hex string.
func GenerateSpanID() string {
	return randomHex(8)
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// EventsToJSON marshals events to a JSON array.
func EventsToJSON(events []internal.Event) (string, error) {
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
