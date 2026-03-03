package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
