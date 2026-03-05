package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/justinpbarnett/virgil/internal/envelope"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Memory is the unified memory record stored in the memories table.
type Memory struct {
	ID         string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Kind       string
	SourcePipe string
	Signal     string
	Content    string
	Tags       []string
}

// Entry represents an explicit memory entry (kind='explicit').
type Entry struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// StateEntry represents a working state entry (kind='working_state').
type StateEntry struct {
	Namespace string    `json:"namespace"`
	Key       string    `json:"key"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InvocationEntry represents an invocation entry (kind='invocation').
type InvocationEntry struct {
	ID        string    `json:"id"`
	Pipe      string    `json:"pipe"`
	Signal    string    `json:"signal"`
	Output    string    `json:"output"`
	CreatedAt time.Time `json:"created_at"`
}

// ContextRequest describes what kind of memory context to retrieve.
type ContextRequest struct {
	Type      string   // "topic_history", "working_state", "user_preferences", "relational"
	Depth     string   // optional duration like "7d", "30d"
	Relations []string // for relational: which edge types to traverse
}

// Store wraps a SQLite database.
type Store struct {
	db *sql.DB

	// prepared statements for hot-path queries
	listAllStateStmt       *sql.Stmt
	searchRankStmt         *sql.Stmt
	searchInvStmt          *sql.Stmt     // no pipe, no since
	searchInvSinceStmt     *sql.Stmt     // no pipe, with since
}

func newID() string {
	return uuid.New().String()
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy timeout: %w", err)
	}
	db.Exec("PRAGMA synchronous=NORMAL")  //nolint:errcheck
	db.Exec("PRAGMA cache_size=-8000")    //nolint:errcheck

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	st := &Store{db: db}
	if err := st.prepareStatements(); err != nil {
		db.Close()
		return nil, fmt.Errorf("preparing statements: %w", err)
	}

	return st, nil
}

func (s *Store) prepareStatements() error {
	var err error

	s.listAllStateStmt, err = s.db.Prepare(
		"SELECT id, content, updated_at FROM memories WHERE kind = 'working_state' ORDER BY updated_at DESC LIMIT 100",
	)
	if err != nil {
		return fmt.Errorf("listAllState: %w", err)
	}

	s.searchRankStmt, err = s.db.Prepare(`
		SELECT m.rowid, m.content, m.tags, m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.kind = 'explicit'
		ORDER BY rank
		LIMIT ?
	`)
	if err != nil {
		return fmt.Errorf("searchRank: %w", err)
	}

	s.searchInvStmt, err = s.db.Prepare(`
		SELECT m.id, m.source_pipe, m.signal, m.content, m.created_at
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.kind = 'invocation'
		ORDER BY rank
		LIMIT ?
	`)
	if err != nil {
		return fmt.Errorf("searchInv: %w", err)
	}

	s.searchInvSinceStmt, err = s.db.Prepare(`
		SELECT m.id, m.source_pipe, m.signal, m.content, m.created_at
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.kind = 'invocation' AND m.created_at >= ?
		ORDER BY rank
		LIMIT ?
	`)
	if err != nil {
		return fmt.Errorf("searchInvSince: %w", err)
	}

	return nil
}

// runMigrations handles schema setup using goose. Goose runs first to create
// (or update) all tables, then any legacy pre-goose data is migrated.
func runMigrations(db *sql.DB) error {
	goose.SetLogger(goose.NopLogger())
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}

	// One-time data migration from the old entries/working_state/invocations schema.
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='entries'").Scan(&count)
	if count > 0 {
		if err := migrateOldSchema(db); err != nil {
			return fmt.Errorf("legacy migration: %w", err)
		}
	}

	return nil
}

// migrateOldSchema copies data from the pre-goose schema into the new memories
// table structure. Runs inside a single transaction and drops the old tables on
// success. Safe to call only when the "entries" table exists.
func migrateOldSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	// Migrate entries → memories (kind='explicit')
	rows, err := tx.Query("SELECT content, tags, created_at, updated_at FROM entries")
	if err != nil {
		return err
	}
	for rows.Next() {
		var content, tagStr string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&content, &tagStr, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return err
		}
		_, err = tx.Exec(
			"INSERT INTO memories (id, created_at, updated_at, kind, content, tags) VALUES (?, ?, ?, 'explicit', ?, ?)",
			newID(), createdAt.UnixNano(), updatedAt.UnixNano(), content, tagStr,
		)
		if err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Migrate working_state → memories (kind='working_state')
	rows, err = tx.Query("SELECT namespace, key, content, created_at, updated_at FROM working_state")
	if err != nil {
		return err
	}
	for rows.Next() {
		var namespace, key, content string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&namespace, &key, &content, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return err
		}
		compositeID := namespace + "/" + key
		_, err = tx.Exec(
			"INSERT INTO memories (id, created_at, updated_at, kind, content) VALUES (?, ?, ?, 'working_state', ?)",
			compositeID, createdAt.UnixNano(), updatedAt.UnixNano(), content,
		)
		if err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Migrate invocations → memories (kind='invocation')
	rows, err = tx.Query("SELECT pipe, signal, output, created_at FROM invocations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var pipeName, signal, output string
		var createdAt time.Time
		if err := rows.Scan(&pipeName, &signal, &output, &createdAt); err != nil {
			rows.Close()
			return err
		}
		_, err = tx.Exec(
			"INSERT INTO memories (id, created_at, updated_at, kind, source_pipe, signal, content) VALUES (?, ?, ?, 'invocation', ?, ?, ?)",
			newID(), createdAt.UnixNano(), createdAt.UnixNano(), pipeName, signal, output,
		)
		if err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, stmt := range []string{
		"DROP TRIGGER IF EXISTS entries_ai",
		"DROP TRIGGER IF EXISTS entries_ad",
		"DROP TRIGGER IF EXISTS entries_au",
		"DROP TRIGGER IF EXISTS invocations_ai",
		"DROP TRIGGER IF EXISTS invocations_ad",
		"DROP TABLE IF EXISTS entries_fts",
		"DROP TABLE IF EXISTS invocations_fts",
		"DROP TABLE IF EXISTS entries",
		"DROP TABLE IF EXISTS working_state",
		"DROP TABLE IF EXISTS invocations",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) Close() error {
	if s.listAllStateStmt != nil {
		s.listAllStateStmt.Close()
	}
	if s.searchRankStmt != nil {
		s.searchRankStmt.Close()
	}
	if s.searchInvStmt != nil {
		s.searchInvStmt.Close()
	}
	if s.searchInvSinceStmt != nil {
		s.searchInvSinceStmt.Close()
	}
	return s.db.Close()
}

// Save inserts an explicit memory entry.
func (s *Store) Save(content string, tags []string) error {
	tagStr := strings.Join(tags, ",")
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO memories (id, created_at, updated_at, kind, content, tags) VALUES (?, ?, ?, 'explicit', ?, ?)",
		newID(), now.UnixNano(), now.UnixNano(), content, tagStr,
	)
	return err
}

// Search performs an FTS search on explicit memory entries.
func (s *Store) Search(query string, limit int, sort string) ([]Entry, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error
	if sort == "recent" {
		rows, err = s.db.Query(`
			SELECT m.rowid, m.content, m.tags, m.created_at, m.updated_at
			FROM memories_fts f
			JOIN memories m ON m.rowid = f.rowid
			WHERE memories_fts MATCH ? AND m.kind = 'explicit'
			ORDER BY m.created_at DESC
			LIMIT ?
		`, query, limit)
	} else {
		rows, err = s.searchRankStmt.Query(query, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var tagStr string
		var createdNano, updatedNano int64
		if err := rows.Scan(&e.ID, &e.Content, &tagStr, &createdNano, &updatedNano); err != nil {
			return nil, err
		}
		e.CreatedAt = time.Unix(0, createdNano)
		e.UpdatedAt = time.Unix(0, updatedNano)
		if tagStr != "" {
			e.Tags = strings.Split(tagStr, ",")
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PutState upserts a working-state entry.
func (s *Store) PutState(namespace, key, content string) error {
	compositeID := namespace + "/" + key
	now := time.Now()
	_, err := s.db.Exec(`
		INSERT INTO memories (id, created_at, updated_at, kind, content)
		VALUES (?, ?, ?, 'working_state', ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at
	`, compositeID, now.UnixNano(), now.UnixNano(), content)
	return err
}

// GetState retrieves a working-state entry by namespace and key.
func (s *Store) GetState(namespace, key string) (string, bool, error) {
	compositeID := namespace + "/" + key
	var content string
	err := s.db.QueryRow(
		"SELECT content FROM memories WHERE id = ? AND kind = 'working_state'",
		compositeID,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

// DeleteState removes a working-state entry.
func (s *Store) DeleteState(namespace, key string) error {
	compositeID := namespace + "/" + key
	_, err := s.db.Exec(
		"DELETE FROM memories WHERE id = ? AND kind = 'working_state'",
		compositeID,
	)
	return err
}

// ListState returns all working-state entries in a namespace, ordered by updated_at DESC.
func (s *Store) ListState(namespace string) ([]StateEntry, error) {
	prefix := namespace + "/"
	rows, err := s.db.Query(
		"SELECT id, content, updated_at FROM memories WHERE kind = 'working_state' AND id LIKE ? ORDER BY updated_at DESC",
		prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []StateEntry
	for rows.Next() {
		var e StateEntry
		var id string
		var updatedNano int64
		if err := rows.Scan(&id, &e.Content, &updatedNano); err != nil {
			return nil, err
		}
		e.Namespace, e.Key = parseStateID(id)
		e.UpdatedAt = time.Unix(0, updatedNano)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// SaveInvocation records a pipe invocation. Returns the new memory ID.
func (s *Store) SaveInvocation(pipe, signal, output string) (string, error) {
	id := newID()
	now := time.Now()
	_, err := s.db.Exec(
		"INSERT INTO memories (id, created_at, updated_at, kind, source_pipe, signal, content) VALUES (?, ?, ?, 'invocation', ?, ?, ?)",
		id, now.UnixNano(), now.UnixNano(), pipe, signal, output,
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

// SearchInvocations performs an FTS search on invocation entries.
func (s *Store) SearchInvocations(query, pipeName string, limit int, since time.Time) ([]InvocationEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	var rows *sql.Rows
	var err error

	// Use prepared statements for the common no-pipe-filter cases
	if pipeName == "" {
		if since.IsZero() {
			rows, err = s.searchInvStmt.Query(query, limit)
		} else {
			rows, err = s.searchInvSinceStmt.Query(query, since.UnixNano(), limit)
		}
	}

	if rows == nil && err == nil {
		// Fall back to dynamic query for filtered cases
		conds := []string{"memories_fts MATCH ?", "m.kind = 'invocation'"}
		args := []any{query}
		if pipeName != "" {
			conds = append(conds, "m.source_pipe = ?")
			args = append(args, pipeName)
		}
		if !since.IsZero() {
			conds = append(conds, "m.created_at >= ?")
			args = append(args, since.UnixNano())
		}
		args = append(args, limit)

		rows, err = s.db.Query(fmt.Sprintf(`
			SELECT m.id, m.source_pipe, m.signal, m.content, m.created_at
			FROM memories_fts f
			JOIN memories m ON m.rowid = f.rowid
			WHERE %s
			ORDER BY rank
			LIMIT ?
		`, strings.Join(conds, " AND ")), args...)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []InvocationEntry
	for rows.Next() {
		var e InvocationEntry
		var createdNano int64
		var sourcePipe, signal sql.NullString
		if err := rows.Scan(&e.ID, &sourcePipe, &signal, &e.Output, &createdNano); err != nil {
			return nil, err
		}
		e.Pipe = sourcePipe.String
		e.Signal = signal.String
		e.CreatedAt = time.Unix(0, createdNano)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// parseStateID splits a composite "namespace/key" ID back into its parts.
func parseStateID(id string) (namespace, key string) {
	parts := strings.SplitN(id, "/", 2)
	namespace = parts[0]
	if len(parts) > 1 {
		key = parts[1]
	}
	return
}

// listAllState returns the most recent working state entries across all namespaces.
func (s *Store) listAllState() ([]StateEntry, error) {
	rows, err := s.listAllStateStmt.Query()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []StateEntry
	for rows.Next() {
		var e StateEntry
		var id string
		var updatedNano int64
		if err := rows.Scan(&id, &e.Content, &updatedNano); err != nil {
			return nil, err
		}
		e.Namespace, e.Key = parseStateID(id)
		e.UpdatedAt = time.Unix(0, updatedNano)
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
// Relational requests are processed last, using anchor IDs from prior retrievals.
func (s *Store) RetrieveContext(query string, requests []ContextRequest, budget int) ([]envelope.MemoryEntry, error) {
	if budget <= 0 {
		budget = 500
	}
	charBudget := budget * 4
	var results []envelope.MemoryEntry
	usedChars := 0
	seenIDs := make(map[string]bool)

	var standardReqs, relationalReqs []ContextRequest
	for _, req := range requests {
		if req.Type == "relational" {
			relationalReqs = append(relationalReqs, req)
		} else {
			standardReqs = append(standardReqs, req)
		}
	}

	for i, req := range standardReqs {
		if usedChars >= charBudget {
			break
		}
		remaining := len(standardReqs) - i
		share := (charBudget - usedChars) / max(1, remaining)

		switch req.Type {
		case "topic_history":
			if query == "" {
				continue
			}
			since := parseDepth(req.Depth)
			entries, err := s.SearchInvocations(query, "", 5, since)
			if err != nil {
				continue
			}
			for _, e := range entries {
				text := e.Signal + " → " + e.Output
				if usedChars+len(text) > charBudget {
					break
				}
				text = truncateRunes(text, share)
				results = append(results, envelope.MemoryEntry{ID: e.ID, Type: "topic_history", Content: text})
				seenIDs[e.ID] = true
				usedChars += len(text)
			}

		case "working_state":
			entries, err := s.listAllState()
			if err != nil {
				continue
			}
			for _, e := range entries {
				if usedChars >= charBudget {
					break
				}
				text := e.Namespace + "/" + e.Key + ": " + e.Content
				text = truncateRunes(text, share)
				if usedChars+len(text) > charBudget {
					break
				}
				id := e.Namespace + "/" + e.Key
				results = append(results, envelope.MemoryEntry{ID: id, Type: "working_state", Content: text})
				seenIDs[id] = true
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

	for _, req := range relationalReqs {
		if usedChars >= charBudget {
			break
		}

		var anchorIDs []string
		for _, m := range results {
			if m.ID != "" {
				anchorIDs = append(anchorIDs, m.ID)
			}
		}
		if len(anchorIDs) == 0 {
			continue
		}

		relations := req.Relations
		if len(relations) == 0 {
			relations = []string{RelationCoOccurred, RelationProducedBy, RelationRefinedFrom}
		}

		connected, err := s.TraverseFrom(anchorIDs, relations, 10)
		if err != nil {
			continue
		}

		share := charBudget - usedChars
		for _, m := range connected {
			if seenIDs[m.ID] {
				continue
			}
			text := truncateRunes(m.Content, share)
			if usedChars+len(text) > charBudget {
				break
			}
			results = append(results, envelope.MemoryEntry{ID: m.ID, Type: "relational", Content: text})
			seenIDs[m.ID] = true
			usedChars += len(text)
		}
	}

	return results, nil
}
