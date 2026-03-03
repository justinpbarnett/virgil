package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justinpbarnett/virgil/internal/envelope"
	_ "modernc.org/sqlite"
)

type Entry struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			content,
			content='entries',
			content_rowid='id'
		);

		CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid, content) VALUES (new.id, new.content);
		END;

		CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, content) VALUES('delete', old.id, old.content);
		END;

		CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, content) VALUES('delete', old.id, old.content);
			INSERT INTO entries_fts(rowid, content) VALUES (new.id, new.content);
		END;

		CREATE TABLE IF NOT EXISTS working_state (
			namespace TEXT NOT NULL,
			key TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (namespace, key)
		);

		CREATE TABLE IF NOT EXISTS invocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pipe TEXT NOT NULL,
			signal TEXT NOT NULL,
			output TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE VIRTUAL TABLE IF NOT EXISTS invocations_fts USING fts5(
			signal,
			output,
			content='invocations',
			content_rowid='id'
		);

		CREATE TRIGGER IF NOT EXISTS invocations_ai AFTER INSERT ON invocations BEGIN
			INSERT INTO invocations_fts(rowid, signal, output) VALUES (new.id, new.signal, new.output);
		END;

		CREATE TRIGGER IF NOT EXISTS invocations_ad AFTER DELETE ON invocations BEGIN
			INSERT INTO invocations_fts(invocations_fts, rowid, signal, output) VALUES('delete', old.id, old.signal, old.output);
		END;
	`)
	return err
}

// StateEntry represents a row in the working_state table.
type StateEntry struct {
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PutState upserts a working-state entry keyed by namespace and key.
func (s *Store) PutState(namespace, key, content string) error {
	now := time.Now()
	_, err := s.db.Exec(`
		INSERT INTO working_state (namespace, key, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(namespace, key) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at
	`, namespace, key, content, now, now)
	return err
}

// GetState retrieves a working-state entry by namespace and key.
// Returns the content, whether it was found, and any error.
func (s *Store) GetState(namespace, key string) (string, bool, error) {
	var content string
	err := s.db.QueryRow(
		"SELECT content FROM working_state WHERE namespace = ? AND key = ?",
		namespace, key,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

// DeleteState removes a working-state entry. No error if it doesn't exist.
func (s *Store) DeleteState(namespace, key string) error {
	_, err := s.db.Exec(
		"DELETE FROM working_state WHERE namespace = ? AND key = ?",
		namespace, key,
	)
	return err
}

// ListState returns all entries in a namespace, ordered by updated_at DESC.
func (s *Store) ListState(namespace string) ([]StateEntry, error) {
	rows, err := s.db.Query(
		"SELECT namespace, key, content, updated_at FROM working_state WHERE namespace = ? ORDER BY updated_at DESC",
		namespace,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []StateEntry
	for rows.Next() {
		var e StateEntry
		if err := rows.Scan(&e.Namespace, &e.Key, &e.Content, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) Save(content string, tags []string) error {
	tagStr := strings.Join(tags, ",")
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO entries (content, tags, created_at, updated_at) VALUES (?, ?, ?, ?)",
		content, tagStr, now, now,
	)
	return err
}

func (s *Store) Search(query string, limit int, sort string) ([]Entry, error) {
	if limit <= 0 {
		limit = 10
	}

	orderClause := "ORDER BY rank"
	if sort == "recent" {
		orderClause = "ORDER BY e.created_at DESC"
	}

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT e.id, e.content, e.tags, e.created_at, e.updated_at
		FROM entries_fts f
		JOIN entries e ON e.id = f.rowid
		WHERE entries_fts MATCH ?
		%s
		LIMIT ?
	`, orderClause), query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var tagStr string
		if err := rows.Scan(&e.ID, &e.Content, &tagStr, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if tagStr != "" {
			e.Tags = strings.Split(tagStr, ",")
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) Close() error {
	return s.db.Close()
}

// InvocationEntry represents a row in the invocations table.
type InvocationEntry struct {
	ID        int64     `json:"id"`
	Pipe      string    `json:"pipe"`
	Signal    string    `json:"signal"`
	Output    string    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
}

// ContextRequest describes what kind of memory context to retrieve.
type ContextRequest struct {
	Type  string // "topic_history", "working_state", "user_preferences"
	Depth string // optional duration like "7d", "30d"
}

// SaveInvocation records a pipe invocation for automatic memory encoding.
func (s *Store) SaveInvocation(pipe, signal, output string) error {
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO invocations (pipe, signal, output, created_at) VALUES (?, ?, ?, ?)",
		pipe, signal, output, now,
	)
	return err
}

// SearchInvocations performs an FTS search on the invocations table.
// pipe filters by pipe name (empty string means any pipe).
// since filters to entries created after that time (zero value means no time filter).
func (s *Store) SearchInvocations(query, pipe string, limit int, since time.Time) ([]InvocationEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	conds := []string{"invocations_fts MATCH ?"}
	args := []any{query}
	if pipe != "" {
		conds = append(conds, "i.pipe = ?")
		args = append(args, pipe)
	}
	if !since.IsZero() {
		conds = append(conds, "i.created_at >= ?")
		args = append(args, since)
	}
	args = append(args, limit)

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT i.id, i.pipe, i.signal, i.output, i.created_at
		FROM invocations_fts f
		JOIN invocations i ON i.id = f.rowid
		WHERE %s
		ORDER BY rank
		LIMIT ?
	`, strings.Join(conds, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []InvocationEntry
	for rows.Next() {
		var e InvocationEntry
		if err := rows.Scan(&e.ID, &e.Pipe, &e.Signal, &e.Output, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// truncateRunes truncates s to at most maxBytes bytes without splitting a UTF-8 rune.
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	last := 0
	for i := range s {
		if i > maxBytes {
			return s[:last]
		}
		last = i
	}
	return s[:last]
}

// listAllState returns the most recent working state entries across all namespaces.
func (s *Store) listAllState() ([]StateEntry, error) {
	rows, err := s.db.Query(
		"SELECT namespace, key, content, updated_at FROM working_state ORDER BY updated_at DESC LIMIT 100",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []StateEntry
	for rows.Next() {
		var e StateEntry
		if err := rows.Scan(&e.Namespace, &e.Key, &e.Content, &e.UpdatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// parseDepth parses a depth string like "7d", "30d", "1d" into a time.Time representing
// the cutoff (now minus depth). Returns zero time if depth is empty or unparseable.
func parseDepth(depth string) time.Time {
	if depth == "" {
		return time.Time{}
	}
	if len(depth) < 2 {
		return time.Time{}
	}
	unit := depth[len(depth)-1]
	n, err := strconv.Atoi(depth[:len(depth)-1])
	if err != nil || n <= 0 {
		return time.Time{}
	}
	switch unit {
	case 'd':
		return time.Now().AddDate(0, 0, -n)
	case 'w':
		return time.Now().AddDate(0, 0, -n*7)
	case 'm':
		return time.Now().AddDate(0, -n, 0)
	default:
		return time.Time{}
	}
}

// RetrieveContext assembles relevant memory entries for a pipe based on context requests.
// Budget is expressed in approximate tokens (4 chars ≈ 1 token).
func (s *Store) RetrieveContext(query string, requests []ContextRequest, budget int) ([]envelope.MemoryEntry, error) {
	if budget <= 0 {
		budget = 500
	}
	charBudget := budget * 4
	var results []envelope.MemoryEntry
	usedChars := 0

	for i, req := range requests {
		if usedChars >= charBudget {
			break
		}
		remaining := len(requests) - i
		share := (charBudget - usedChars) / max(1, remaining)

		switch req.Type {
		case "topic_history":
			if query == "" {
				continue
			}
			since := parseDepth(req.Depth)
			limit := 5
			entries, err := s.SearchInvocations(query, "", limit, since)
			if err != nil {
				continue
			}
			for _, e := range entries {
				text := e.Signal + " → " + e.Output
				if usedChars+len(text) > charBudget {
					break
				}
				text = truncateRunes(text, share)
				results = append(results, envelope.MemoryEntry{Type: "topic_history", Content: text})
				usedChars += len(text)
			}

		case "working_state":
			entries, err := s.listAllState()
			if err != nil {
				continue
			}
			var parts []string
			for _, e := range entries {
				parts = append(parts, e.Namespace+"/"+e.Key+": "+e.Content)
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				continue
			}
			text = truncateRunes(text, share)
			if usedChars+len(text) <= charBudget {
				results = append(results, envelope.MemoryEntry{Type: "working_state", Content: text})
				usedChars += len(text)
			}

		case "user_preferences":
			if query == "" {
				continue
			}
			entries, err := s.Search(query+" preferences", 3, "")
			if err != nil {
				continue
			}
			var parts []string
			for _, e := range entries {
				parts = append(parts, e.Content)
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				continue
			}
			text = truncateRunes(text, share)
			if usedChars+len(text) <= charBudget {
				results = append(results, envelope.MemoryEntry{Type: "user_preferences", Content: text})
				usedChars += len(text)
			}
		}
	}

	return results, nil
}

