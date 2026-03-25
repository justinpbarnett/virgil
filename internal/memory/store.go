package memory

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/justinpbarnett/virgil/internal/db"
)

// Memory type constants.
const (
	TypeObservation = "observation"
	TypeInteraction = "interaction"
	TypeFact        = "fact"
)

// Entity role constants.
const (
	RoleSubject     = "subject"
	RoleMentioned   = "mentioned"
	RoleOwner       = "owner"
	RoleParticipant = "participant"
)

// Default scope when none is specified.
const DefaultScope = "personal"

// Store provides CRUD operations on the memory table.
type Store struct {
	database *sql.DB
}

// NewStore creates a memory Store backed by the given database.
func NewStore(database *sql.DB) *Store {
	return &Store{database: database}
}

// Entry represents a memory row returned from queries.
type Entry struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Scope     string   `json:"scope"`
	Topic     string   `json:"topic,omitempty"`
	Content   string   `json:"content"`
	Source    string   `json:"source,omitempty"`
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Entities  []Entity `json:"entities,omitempty"`
}

// Entity represents a reference extracted from memory content.
type Entity struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Role string `json:"role,omitempty"`
}

// StoreParams is the input for storing a memory.
type StoreParams struct {
	Type     string
	Content  string
	Topic    string
	Scope    string
	Source   string
	Entities []Entity
}

// Store writes a memory entry and its entities. For facts, updates existing
// facts with the same topic+entity instead of creating duplicates.
func (s *Store) Store(p StoreParams) (string, error) {
	switch p.Type {
	case TypeObservation, TypeInteraction, TypeFact:
	default:
		return "", fmt.Errorf("unknown memory type %q", p.Type)
	}
	if p.Scope == "" {
		p.Scope = DefaultScope
	}

	if p.Type == TypeFact && p.Topic != "" {
		existingID, err := s.findExistingFact(p.Topic, p.Scope, p.Entities)
		if err != nil {
			return "", fmt.Errorf("check existing fact: %w", err)
		}
		if existingID != "" {
			return s.updateFact(existingID, p)
		}
	}

	id := uuid.New().String()

	tx, err := s.database.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO memory (id, type, scope, topic, content, source)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, p.Type, p.Scope, db.NullStr(p.Topic), p.Content, db.NullStr(p.Source),
	); err != nil {
		return "", fmt.Errorf("insert memory: %w", err)
	}

	for _, e := range p.Entities {
		role := e.Role
		if role == "" {
			role = RoleMentioned
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO memory_entities (memory_id, entity, entity_type, role)
			VALUES (?, ?, ?, ?)`,
			id, e.Name, db.NullStr(e.Type), role,
		); err != nil {
			return "", fmt.Errorf("insert entity %q: %w", e.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit memory: %w", err)
	}

	return id, nil
}

// Get retrieves a single memory entry by ID.
func (s *Store) Get(id string) (*Entry, error) {
	row := s.database.QueryRow(`
		SELECT id, type, scope, topic, content, source, created_at, expires_at
		FROM memory WHERE id = ?`, id)

	e := &Entry{}
	var topic, source, expiresAt sql.NullString
	if err := row.Scan(&e.ID, &e.Type, &e.Scope, &topic, &e.Content, &source, &e.CreatedAt, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	e.Topic = topic.String
	e.Source = source.String
	e.ExpiresAt = expiresAt.String

	entities, err := s.getEntities(e.ID)
	if err != nil {
		return nil, err
	}
	e.Entities = entities

	return e, nil
}

func (s *Store) findExistingFact(topic string, scope string, entities []Entity) (string, error) {
	if len(entities) == 0 {
		row := s.database.QueryRow(`
			SELECT id FROM memory WHERE type = ? AND topic = ? AND scope = ? LIMIT 1`, TypeFact, topic, scope)
		var id string
		if err := row.Scan(&id); err != nil {
			if err == sql.ErrNoRows {
				return "", nil
			}
			return "", err
		}
		return id, nil
	}

	row := s.database.QueryRow(`
		SELECT m.id FROM memory m
		JOIN memory_entities me ON m.id = me.memory_id
		WHERE m.type = ? AND m.topic = ? AND m.scope = ? AND me.entity = ?
		LIMIT 1`, TypeFact, topic, scope, entities[0].Name)
	var id string
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

func (s *Store) updateFact(id string, p StoreParams) (string, error) {
	tx, err := s.database.Begin()
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var oldContent string
	if err := tx.QueryRow("SELECT content FROM memory WHERE id = ?", id).Scan(&oldContent); err != nil {
		return "", fmt.Errorf("read old fact: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE memory SET content = ?, topic = ?, scope = ?, source = ?
		WHERE id = ?`,
		p.Content, db.NullStr(p.Topic), p.Scope, db.NullStr(p.Source), id,
	); err != nil {
		return "", fmt.Errorf("update fact: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT INTO fact_history (fact_id, old_content, new_content, reason)
		VALUES (?, ?, ?, 'updated')`,
		id, oldContent, p.Content,
	); err != nil {
		return "", fmt.Errorf("write fact history: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM memory_entities WHERE memory_id = ?", id); err != nil {
		return "", fmt.Errorf("delete old entities: %w", err)
	}

	for _, e := range p.Entities {
		role := e.Role
		if role == "" {
			role = RoleMentioned
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO memory_entities (memory_id, entity, entity_type, role)
			VALUES (?, ?, ?, ?)`,
			id, e.Name, db.NullStr(e.Type), role,
		); err != nil {
			return "", fmt.Errorf("insert entity %q: %w", e.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit fact update: %w", err)
	}

	return id, nil
}

func (s *Store) getEntities(memoryID string) ([]Entity, error) {
	rows, err := s.database.Query(`
		SELECT entity, entity_type, role FROM memory_entities
		WHERE memory_id = ?`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []Entity
	for rows.Next() {
		var e Entity
		var eType sql.NullString
		if err := rows.Scan(&e.Name, &eType, &e.Role); err != nil {
			return nil, err
		}
		e.Type = eType.String
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

// Facts returns all facts matching a topic or entity name.
func (s *Store) Facts(about string, scope string) ([]Entry, error) {
	q := `
		SELECT DISTINCT m.id, m.type, m.scope, m.topic, m.content, m.source, m.created_at, m.expires_at
		FROM memory m
		LEFT JOIN memory_entities me ON m.id = me.memory_id
		WHERE m.type = ?
		AND (m.topic LIKE ? OR me.entity LIKE ?)`
	args := []any{TypeFact, "%" + about + "%", "%" + about + "%"}

	if scope != "" {
		q += " AND m.scope = ?"
		args = append(args, scope)
	}
	q += " ORDER BY m.created_at DESC LIMIT 100"

	return s.queryEntries(q, args...)
}

// FactsBatch returns all facts matching any of the given entities in a single query.
func (s *Store) FactsBatch(entities []string, scope string) (map[string][]Entry, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	var conditions []string
	var args []any
	for _, e := range entities {
		conditions = append(conditions, "(m.topic LIKE ? OR me.entity LIKE ?)")
		args = append(args, "%"+e+"%", "%"+e+"%")
	}

	q := fmt.Sprintf(`
		SELECT DISTINCT m.id, m.type, m.scope, m.topic, m.content, m.source, m.created_at, m.expires_at
		FROM memory m
		LEFT JOIN memory_entities me ON m.id = me.memory_id
		WHERE m.type = ?
		AND (%s)`, strings.Join(conditions, " OR "))
	args = append([]any{TypeFact}, args...)

	if scope != "" {
		q += " AND m.scope = ?"
		args = append(args, scope)
	}
	q += " ORDER BY m.created_at DESC LIMIT 100"

	entries, err := s.queryEntries(q, args...)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]Entry)
	for _, entry := range entries {
		for _, e := range entities {
			if entryMatchesEntity(entry, e) {
				result[e] = append(result[e], entry)
			}
		}
	}
	return result, nil
}

func entryMatchesEntity(entry Entry, entity string) bool {
	eLower := strings.ToLower(entity)
	if strings.Contains(strings.ToLower(entry.Topic), eLower) ||
		strings.Contains(strings.ToLower(entry.Content), eLower) {
		return true
	}
	for _, ent := range entry.Entities {
		if strings.Contains(strings.ToLower(ent.Name), eLower) {
			return true
		}
	}
	return false
}

func (s *Store) queryEntries(q string, args ...any) ([]Entry, error) {
	rows, err := s.database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		var topic, source, expiresAt sql.NullString
		if err := rows.Scan(&e.ID, &e.Type, &e.Scope, &topic, &e.Content, &source, &e.CreatedAt, &expiresAt); err != nil {
			return nil, err
		}
		e.Topic = topic.String
		e.Source = source.String
		e.ExpiresAt = expiresAt.String
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range entries {
		entities, err := s.getEntities(entries[i].ID)
		if err != nil {
			return nil, err
		}
		entries[i].Entities = entities
	}
	return entries, nil
}
