package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Confidence float64
	ValidUntil *time.Time // nil = never expires
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
	listAllStateStmt   *sql.Stmt
	searchRankStmt     *sql.Stmt
	searchInvStmt      *sql.Stmt // no pipe, no since
	searchInvSinceStmt *sql.Stmt // no pipe, with since
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
	db.Exec("PRAGMA synchronous=NORMAL") //nolint:errcheck
	db.Exec("PRAGMA cache_size=-8000")   //nolint:errcheck

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
		"SELECT id, content, updated_at FROM memories WHERE kind = 'working_state' AND (valid_until IS NULL OR valid_until > ?) ORDER BY updated_at DESC LIMIT 100",
	)
	if err != nil {
		return fmt.Errorf("listAllState: %w", err)
	}

	s.searchRankStmt, err = s.db.Prepare(`
		SELECT m.rowid, m.content, m.tags, m.created_at, m.updated_at
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE memories_fts MATCH ? AND m.kind = 'explicit'
		  AND (m.valid_until IS NULL OR m.valid_until > ?)
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
		  AND (m.valid_until IS NULL OR m.valid_until > ?)
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
		  AND (m.valid_until IS NULL OR m.valid_until > ?)
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
	// All three old tables must exist for the full migration to run.
	var oldTableCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('entries','working_state','invocations')").Scan(&oldTableCount)
	if oldTableCount == 3 {
		if err := migrateOldSchema(db); err != nil {
			return fmt.Errorf("legacy migration: %w", err)
		}
	} else if oldTableCount > 0 {
		// Partial old schema remnants — clean up whatever is left.
		dropOrphanedOldTables(db)
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

	for _, stmt := range oldSchemaDropStatements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// oldSchemaDropStatements lists the DDL needed to remove the pre-goose schema.
// Order matters: triggers before FTS shadow tables before base tables.
var oldSchemaDropStatements = []string{
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
}

// dropOrphanedOldTables removes leftover pre-goose tables that can't be fully
// migrated (because not all three old tables are present). Safe to call multiple
// times — each statement uses IF EXISTS.
func dropOrphanedOldTables(db *sql.DB) {
	for _, stmt := range oldSchemaDropStatements {
		db.Exec(stmt) //nolint:errcheck
	}
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

// Save inserts an explicit memory entry with optional expiry.
func (s *Store) Save(content string, tags []string, validUntil *time.Time) error {
	tagStr := strings.Join(tags, ",")
	now := time.Now()
	id := newID()

	var validUntilNano any
	if validUntil != nil {
		validUntilNano = validUntil.UnixNano()
	}

	_, err := s.db.Exec(
		"INSERT INTO memories (id, created_at, updated_at, kind, content, tags, confidence, valid_until) VALUES (?, ?, ?, 'explicit', ?, ?, ?, ?)",
		id, now.UnixNano(), now.UnixNano(), content, tagStr, ConfidenceExplicit, validUntilNano,
	)
	if err != nil {
		return err
	}

	return s.detectSupersession(id, content, tags)
}

// detectSupersession checks for existing explicit memories that may be
// contradicted by the new memory. If found, creates refined_from edges and
// halves the old memory's confidence. At most 3 supersessions per save.
func (s *Store) detectSupersession(newID, content string, tags []string) error {
	now := time.Now().UnixNano()

	type candidate struct {
		id      string
		content string
	}
	seen := make(map[string]bool)
	var candidates []candidate

	// Find candidates via tag overlap
	if len(tags) > 0 {
		conds := make([]string, len(tags))
		args := make([]any, 0, len(tags)+2)
		args = append(args, newID, now)
		for i, tag := range tags {
			conds[i] = "','||tags||',' LIKE '%,'||?||',%'"
			args = append(args, tag)
		}
		args = append(args, 3)

		query := fmt.Sprintf(`
			SELECT id, content FROM memories
			WHERE kind = 'explicit'
			  AND id != ?
			  AND (valid_until IS NULL OR valid_until > ?)
			  AND (%s)
			LIMIT ?
		`, strings.Join(conds, " OR "))

		rows, err := s.db.Query(query, args...)
		if err == nil {
			for rows.Next() {
				var id, c string
				if rows.Scan(&id, &c) == nil && !seen[id] {
					seen[id] = true
					candidates = append(candidates, candidate{id, c})
				}
			}
			rows.Close()
		}
	}

	// FTS search for similar content (best-effort; FTS errors are silently ignored).
	// Quote as FTS phrase to avoid syntax errors from operator chars in content.
	ftsPhrase := `"` + strings.ReplaceAll(content, `"`, `""`) + `"`
	ftsRows, err := s.db.Query(`
		SELECT m.id, m.content
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE memories_fts MATCH ?
		  AND m.kind = 'explicit'
		  AND m.id != ?
		  AND (m.valid_until IS NULL OR m.valid_until > ?)
		ORDER BY rank
		LIMIT 3
	`, ftsPhrase, newID, now)
	if err == nil {
		for ftsRows.Next() {
			var id, c string
			if ftsRows.Scan(&id, &c) == nil && !seen[id] {
				seen[id] = true
				candidates = append(candidates, candidate{id, c})
			}
		}
		ftsRows.Close()
	}

	// Process candidates (at most 3 supersessions per save)
	processed := 0
	for _, cand := range candidates {
		if processed >= 3 {
			break
		}
		if cand.content == content {
			continue // identical content — duplicate, not contradiction
		}

		if err := s.CreateEdge(Edge{
			SourceID: newID,
			TargetID: cand.id,
			Relation: RelationRefinedFrom,
		}); err != nil {
			continue
		}

		s.db.Exec( //nolint:errcheck
			"UPDATE memories SET confidence = MAX(confidence * 0.5, 0.01) WHERE id = ?",
			cand.id,
		)
		processed++
	}

	return nil
}

// supersededIDs returns the set of memory IDs (from the given list) that are
// targets of a refined_from edge (i.e., they have been superseded).
func (s *Store) supersededIDs(ids []string) (map[string]bool, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT target_id FROM memory_edges
		WHERE relation = 'refined_from'
		  AND target_id IN (%s)
	`, placeholders(len(ids)))

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// Search performs an FTS search on explicit memory entries.
func (s *Store) Search(query string, limit int, sortOrder string) ([]Entry, error) {
	if limit <= 0 {
		limit = 10
	}

	now := time.Now().UnixNano()

	var rows *sql.Rows
	var err error
	if sortOrder == "recent" {
		rows, err = s.db.Query(`
			SELECT m.rowid, m.content, m.tags, m.created_at, m.updated_at
			FROM memories_fts f
			JOIN memories m ON m.rowid = f.rowid
			WHERE memories_fts MATCH ? AND m.kind = 'explicit'
			  AND (m.valid_until IS NULL OR m.valid_until > ?)
			ORDER BY m.created_at DESC
			LIMIT ?
		`, query, now, limit)
	} else {
		rows, err = s.searchRankStmt.Query(query, now, limit)
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
		INSERT INTO memories (id, created_at, updated_at, kind, content, confidence)
		VALUES (?, ?, ?, 'working_state', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at
	`, compositeID, now.UnixNano(), now.UnixNano(), content, ConfidenceWorkingState)
	return err
}

// GetState retrieves a working-state entry by namespace and key.
func (s *Store) GetState(namespace, key string) (string, bool, error) {
	compositeID := namespace + "/" + key
	var content string
	err := s.db.QueryRow(
		"SELECT content FROM memories WHERE id = ? AND kind = 'working_state' AND (valid_until IS NULL OR valid_until > ?)",
		compositeID, time.Now().UnixNano(),
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
		"SELECT id, content, updated_at FROM memories WHERE kind = 'working_state' AND id LIKE ? AND (valid_until IS NULL OR valid_until > ?) ORDER BY updated_at DESC LIMIT 100",
		prefix+"%", time.Now().UnixNano(),
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
		"INSERT INTO memories (id, created_at, updated_at, kind, source_pipe, signal, content, confidence) VALUES (?, ?, ?, 'invocation', ?, ?, ?, ?)",
		id, now.UnixNano(), now.UnixNano(), pipe, signal, output, ConfidenceInvocation,
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

	now := time.Now().UnixNano()

	var rows *sql.Rows
	var err error

	// Use prepared statements for the common no-pipe-filter cases
	if pipeName == "" {
		if since.IsZero() {
			rows, err = s.searchInvStmt.Query(query, now, limit)
		} else {
			rows, err = s.searchInvSinceStmt.Query(query, since.UnixNano(), now, limit)
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
		conds = append(conds, "(m.valid_until IS NULL OR m.valid_until > ?)")
		args = append(args, now)
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
	return scanInvocations(rows)
}

// scanInvocations reads invocation entries from rows that select
// (id, source_pipe, signal, content, created_at).
func scanInvocations(rows *sql.Rows) ([]InvocationEntry, error) {
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

// RecentInvocations returns the most recent invocations ordered by time, regardless of content match.
func (s *Store) RecentInvocations(limit int) ([]InvocationEntry, error) {
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.Query(`
		SELECT id, source_pipe, signal, content, created_at
		FROM memories
		WHERE kind = 'invocation'
		  AND (valid_until IS NULL OR valid_until > ?)
		ORDER BY created_at DESC
		LIMIT ?
	`, time.Now().UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInvocations(rows)
}

// Todo status values.
const (
	TodoStatusPending = "pending"
	TodoStatusDone    = "done"
)

// todoCols is the shared column list for all todo SELECT queries.
const todoCols = `id, title, status, priority, COALESCE(due_date,''), tags, COALESCE(memory_id,''), COALESCE(external_id,''), COALESCE(details,''), created_at, COALESCE(completed_at,0)`

// Todo represents a todo list item.
type Todo struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Priority    int       `json:"priority"`
	DueDate     string    `json:"due_date,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	MemoryID    string    `json:"memory_id,omitempty"`
	ExternalID  string    `json:"external_id,omitempty"`
	Details     string    `json:"details,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

// clampPriority restricts a priority value to the valid range [1, 5].
func clampPriority(p int) int {
	if p < 1 {
		return 1
	}
	if p > 5 {
		return 5
	}
	return p
}

// AddTodo inserts a new todo item and returns the created Todo.
func (s *Store) AddTodo(title string, priority int, dueDate string, tags []string) (Todo, error) {
	priority = clampPriority(priority)

	id := newID()
	now := time.Now()
	tagStr := strings.Join(tags, ",")

	_, err := s.db.Exec(
		`INSERT INTO todos (id, title, status, priority, due_date, tags, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, title, TodoStatusPending, priority, dueDate, tagStr, now.UnixNano(),
	)
	if err != nil {
		return Todo{}, err
	}

	return Todo{
		ID:        id,
		Title:     title,
		Status:    TodoStatusPending,
		Priority:  priority,
		DueDate:   dueDate,
		Tags:      tags,
		CreatedAt: now,
	}, nil
}

// ListTodos returns todos ordered by priority ASC, created_at ASC.
// When status is empty or "all", returns all items. Default limit is 25.
func (s *Store) ListTodos(status string, limit int) ([]Todo, error) {
	if limit <= 0 {
		limit = 25
	}

	q := "SELECT " + todoCols + " FROM todos"
	args := []any{}
	if status != "" && status != "all" {
		q += " WHERE status = ?"
		args = append(args, status)
	}
	q += " ORDER BY priority ASC, created_at ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTodos(rows)
}

// GetTodo returns a single todo by ID.
func (s *Store) GetTodo(id string) (Todo, error) {
	row := s.db.QueryRow("SELECT "+todoCols+" FROM todos WHERE id = ?", id)
	return scanTodoFrom(row)
}

// FindTodoByExternalID returns a todo by its external_id. Returns sql.ErrNoRows if not found.
func (s *Store) FindTodoByExternalID(externalID string) (Todo, error) {
	row := s.db.QueryRow("SELECT "+todoCols+" FROM todos WHERE external_id = ?", externalID)
	return scanTodoFrom(row)
}

// UpsertTodoByExternalID inserts a todo if the external_id doesn't exist, or updates it if it does.
// Returns the todo and true if it was created, false if it was updated.
func (s *Store) UpsertTodoByExternalID(externalID, title, details string, priority int, dueDate string, tags []string) (Todo, bool, error) {
	if externalID == "" {
		return Todo{}, false, fmt.Errorf("external_id is required for upsert")
	}
	priority = clampPriority(priority)
	id := newID()
	now := time.Now()
	tagStr := strings.Join(tags, ",")

	_, err := s.db.Exec(`
		INSERT INTO todos (id, title, status, priority, due_date, tags, external_id, details, created_at)
		VALUES (?, ?, 'pending', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(external_id) DO UPDATE SET
			title    = excluded.title,
			details  = excluded.details,
			priority = excluded.priority,
			due_date = excluded.due_date,
			tags     = excluded.tags
	`, id, title, priority, dueDate, tagStr, externalID, details, now.UnixNano())
	if err != nil {
		return Todo{}, false, err
	}

	todo, err := s.FindTodoByExternalID(externalID)
	if err != nil {
		return Todo{}, false, err
	}
	created := todo.ID == id
	return todo, created, nil
}

// UpdateTodo updates specified fields on a todo. Supported keys: title, priority, due_date, tags.
func (s *Store) UpdateTodo(id string, updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}

	setClauses := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)

	for k, v := range updates {
		switch k {
		case "title", "due_date", "tags", "details", "external_id":
			setClauses = append(setClauses, k+" = ?")
			args = append(args, v)
		case "priority":
			p, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid priority %q: %w", v, err)
			}
			setClauses = append(setClauses, "priority = ?")
			args = append(args, clampPriority(p))
		}
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE todos SET %s WHERE id = ?", strings.Join(setClauses, ", "))
	_, err := s.db.Exec(query, args...)
	return err
}

// CompleteTodo marks a todo as done.
func (s *Store) CompleteTodo(id string) error {
	_, err := s.db.Exec(
		"UPDATE todos SET status = ?, completed_at = ? WHERE id = ?",
		TodoStatusDone,
		time.Now().UnixNano(), id,
	)
	return err
}

// UncompleteTodo marks a completed todo as pending again.
func (s *Store) UncompleteTodo(id string) error {
	_, err := s.db.Exec(
		"UPDATE todos SET status = ?, completed_at = 0 WHERE id = ?",
		TodoStatusPending, id,
	)
	return err
}

// DeleteTodo removes a todo by ID.
func (s *Store) DeleteTodo(id string) error {
	_, err := s.db.Exec("DELETE FROM todos WHERE id = ?", id)
	return err
}

// ReorderTodo changes a todo's priority.
func (s *Store) ReorderTodo(id string, newPriority int) error {
	_, err := s.db.Exec("UPDATE todos SET priority = ? WHERE id = ?", clampPriority(newPriority), id)
	return err
}

// SetTodoMemoryID links a todo to its creation memory node.
func (s *Store) SetTodoMemoryID(id, memoryID string) error {
	_, err := s.db.Exec("UPDATE todos SET memory_id = ? WHERE id = ?", memoryID, id)
	return err
}

// ListTodosWithExternalIDPrefix returns all todos whose external_id starts with prefix.
func (s *Store) ListTodosWithExternalIDPrefix(prefix string) ([]Todo, error) {
	rows, err := s.db.Query(
		"SELECT "+todoCols+" FROM todos WHERE external_id LIKE ? ORDER BY created_at ASC",
		prefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTodos(rows)
}

func scanTodos(rows *sql.Rows) ([]Todo, error) {
	var todos []Todo
	for rows.Next() {
		t, err := scanTodoFrom(rows)
		if err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

type todoScanner interface {
	Scan(dest ...any) error
}

func scanTodoFrom(s todoScanner) (Todo, error) {
	var t Todo
	var tagStr, memoryID, externalID, details string
	var createdNano, completedNano int64
	if err := s.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.DueDate, &tagStr, &memoryID, &externalID, &details, &createdNano, &completedNano); err != nil {
		return Todo{}, err
	}
	t.MemoryID = memoryID
	t.ExternalID = externalID
	t.Details = details
	t.CreatedAt = time.Unix(0, createdNano)
	if completedNano != 0 {
		t.CompletedAt = time.Unix(0, completedNano)
	}
	if tagStr != "" {
		t.Tags = strings.Split(tagStr, ",")
	}
	return t, nil
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
	rows, err := s.listAllStateStmt.Query(time.Now().UnixNano())
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
// Results are gathered from all sources, scored with a composite function, and
// selected by score until the budget is exhausted.
func (s *Store) RetrieveContext(query string, requests []ContextRequest, budget int) ([]envelope.MemoryEntry, error) {
	if budget <= 0 {
		budget = 500
	}
	charBudget := budget * 4

	var standardReqs, relationalReqs []ContextRequest
	for _, req := range requests {
		if req.Type == "relational" {
			relationalReqs = append(relationalReqs, req)
		} else {
			standardReqs = append(standardReqs, req)
		}
	}

	// Phase 1: Gather candidates from all standard sources.
	var candidates []ScoredMemory
	seenIDs := make(map[string]bool)

	for _, req := range standardReqs {
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
				if seenIDs[e.ID] {
					continue
				}
				text := e.Signal + " → " + e.Output
				candidates = append(candidates, ScoredMemory{
					Memory:      Memory{ID: e.ID, CreatedAt: e.CreatedAt, Kind: "invocation", Content: text, Confidence: ConfidenceInvocation},
					HopDistance: 0,
					SourceType:  "topic_history",
				})
				seenIDs[e.ID] = true
			}

		case "working_state":
			entries, err := s.listAllState()
			if err != nil {
				continue
			}
			for _, e := range entries {
				id := e.Namespace + "/" + e.Key
				if seenIDs[id] {
					continue
				}
				text := e.Namespace + "/" + e.Key + ": " + e.Content
				candidates = append(candidates, ScoredMemory{
					Memory:      Memory{ID: id, CreatedAt: e.UpdatedAt, Kind: "working_state", Content: text, Confidence: ConfidenceWorkingState},
					HopDistance: 0,
					SourceType:  "working_state",
				})
				seenIDs[id] = true
			}

		case "recent_history":
			entries, err := s.RecentInvocations(3)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if seenIDs[e.ID] {
					continue
				}
				text := e.Signal + " → " + e.Output
				candidates = append(candidates, ScoredMemory{
					Memory:      Memory{ID: e.ID, CreatedAt: e.CreatedAt, Kind: "invocation", Content: text, Confidence: ConfidenceInvocation},
					HopDistance: 0,
					SourceType:  "recent_history",
				})
				seenIDs[e.ID] = true
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
			var firstCreatedAt time.Time
			for _, e := range entries {
				parts = append(parts, e.Content)
				if firstCreatedAt.IsZero() {
					firstCreatedAt = e.CreatedAt
				}
			}
			text := strings.Join(parts, "\n")
			if text == "" {
				continue
			}
			if firstCreatedAt.IsZero() {
				firstCreatedAt = time.Now()
			}
			candidates = append(candidates, ScoredMemory{
				Memory:      Memory{CreatedAt: firstCreatedAt, Kind: "explicit", Content: text, Confidence: ConfidenceExplicit},
				HopDistance: 0,
				SourceType:  "user_preferences",
			})
		}
	}

	// Process relational requests using anchor IDs from gathered candidates.
	for _, req := range relationalReqs {
		var anchorIDs []string
		for _, c := range candidates {
			if c.ID != "" {
				anchorIDs = append(anchorIDs, c.ID)
			}
		}
		if len(anchorIDs) == 0 {
			continue
		}

		relations := req.Relations
		if len(relations) == 0 {
			relations = []string{RelationCoOccurred, RelationProducedBy, RelationRefinedFrom}
		}

		hop1, hop2, err := s.TraverseHops(anchorIDs, relations, 10)
		if err != nil {
			continue
		}

		for _, m := range hop1 {
			if seenIDs[m.ID] {
				continue
			}
			candidates = append(candidates, ScoredMemory{
				Memory:      m,
				HopDistance: 1,
				SourceType:  "relational",
			})
			seenIDs[m.ID] = true
		}
		for _, m := range hop2 {
			if seenIDs[m.ID] {
				continue
			}
			candidates = append(candidates, ScoredMemory{
				Memory:      m,
				HopDistance: 2,
				SourceType:  "relational",
			})
			seenIDs[m.ID] = true
		}
	}

	// Phase 2: Score — look up superseded IDs and apply composite scoring.
	var candidateIDs []string
	for _, c := range candidates {
		if c.ID != "" {
			candidateIDs = append(candidateIDs, c.ID)
		}
	}
	superseded, _ := s.supersededIDs(candidateIDs)

	for i := range candidates {
		if superseded[candidates[i].ID] {
			candidates[i].Confidence *= 0.5
		}
		candidates[i].ComputeScore(defaultRelevance(candidates[i].SourceType))
	}

	// Phase 3: Select — sort by score and trim to budget.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Phase 4: Format — convert to MemoryEntry slice.
	var results []envelope.MemoryEntry
	usedChars := 0

	for _, c := range candidates {
		if usedChars >= charBudget {
			break
		}
		text := truncateRunes(c.Content, charBudget-usedChars)
		results = append(results, envelope.MemoryEntry{ID: c.ID, Type: c.SourceType, Content: text})
		usedChars += len(text)
	}

	return results, nil
}
