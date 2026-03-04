package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	RelationCoOccurred  = "co_occurred"
	RelationProducedBy  = "produced_by"
	RelationRefinedFrom = "refined_from"
)

// placeholders returns n comma-separated "?" parameters for SQL IN clauses.
func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	s := strings.Repeat("?,", n)
	return s[:len(s)-1]
}

// Edge represents a typed relationship between two memory entries.
type Edge struct {
	ID        string
	SourceID  string
	TargetID  string
	Relation  string
	Strength  float64
	CreatedAt time.Time
	Context   string
}

const edgeSchemaDDL = `
CREATE TABLE IF NOT EXISTS memory_edges (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation TEXT NOT NULL,
    strength REAL NOT NULL DEFAULT 1.0,
    created_at INTEGER NOT NULL,
    context TEXT,
    UNIQUE(source_id, target_id, relation)
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON memory_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_edges_target ON memory_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_edges_relation ON memory_edges(relation);
`

// createEdgeSchema creates the memory_edges table. Accepts either *sql.DB or *sql.Tx
// via a common Exec interface.
func createEdgeSchema(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}) error {
	_, err := exec.Exec(edgeSchemaDDL)
	return err
}

// CreateEdge inserts or increments an edge. Self-referential edges are silently ignored.
func (s *Store) CreateEdge(edge Edge) error {
	if edge.SourceID == edge.TargetID {
		return nil
	}
	if edge.ID == "" {
		edge.ID = newID()
	}
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = time.Now()
	}
	if edge.Strength == 0 {
		edge.Strength = 1.0
	}

	_, err := s.db.Exec(`
		INSERT INTO memory_edges (id, source_id, target_id, relation, strength, created_at, context)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, target_id, relation) DO UPDATE SET
			strength = strength + 1
	`, edge.ID, edge.SourceID, edge.TargetID, edge.Relation, edge.Strength, edge.CreatedAt.Unix(), edge.Context)
	return err
}

// CreateEdges batch-inserts edges in a single transaction.
func (s *Store) CreateEdges(edges []Edge) error {
	if len(edges) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for _, edge := range edges {
		if edge.SourceID == edge.TargetID {
			continue
		}
		if edge.ID == "" {
			edge.ID = newID()
		}
		createdAt := now
		if !edge.CreatedAt.IsZero() {
			createdAt = edge.CreatedAt.Unix()
		}
		strength := edge.Strength
		if strength == 0 {
			strength = 1.0
		}

		_, err := tx.Exec(`
			INSERT INTO memory_edges (id, source_id, target_id, relation, strength, created_at, context)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_id, target_id, relation) DO UPDATE SET
				strength = strength + 1
		`, edge.ID, edge.SourceID, edge.TargetID, edge.Relation, strength, createdAt, edge.Context)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// TraverseFrom performs a one-hop graph traversal from the given anchor memory IDs.
// Returns connected memories (not the anchors themselves), deduplicated and sorted by
// edge strength descending.
func (s *Store) TraverseFrom(anchorIDs []string, relations []string, limit int) ([]Memory, error) {
	if len(anchorIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	anchorPlaceholders := placeholders(len(anchorIDs))

	relationFilter := ""
	if len(relations) > 0 {
		relationFilter = fmt.Sprintf("AND e.relation IN (%s)", placeholders(len(relations)))
	}

	// Args must match SQL placeholder order:
	// 1. source_id IN (%s) — JOIN ON clause
	// 2. target_id IN (%s) — JOIN ON clause
	// 3. NOT IN (%s)       — WHERE clause
	// 4. relation IN (%s)  — WHERE clause (inside relationFilter)
	// 5. LIMIT ?
	args := make([]any, 0)
	for _, id := range anchorIDs {
		args = append(args, id) // source_id IN
	}
	for _, id := range anchorIDs {
		args = append(args, id) // target_id IN
	}
	for _, id := range anchorIDs {
		args = append(args, id) // NOT IN
	}
	for _, r := range relations {
		args = append(args, r) // relation IN
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT m.id, m.created_at, m.updated_at, m.kind,
		       COALESCE(m.source_pipe, ''), COALESCE(m.signal, ''), m.content, COALESCE(m.tags, ''),
		       MAX(e.strength) as max_strength
		FROM memory_edges e
		JOIN memories m ON (
		    (e.source_id IN (%s) AND e.target_id = m.id)
		    OR
		    (e.target_id IN (%s) AND e.source_id = m.id)
		)
		WHERE m.id NOT IN (%s)
		%s
		GROUP BY m.id
		ORDER BY max_strength DESC
		LIMIT ?
	`, anchorPlaceholders, anchorPlaceholders, anchorPlaceholders, relationFilter)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var createdUnix, updatedUnix int64
		var tagStr string
		var maxStrength float64
		if err := rows.Scan(
			&m.ID, &createdUnix, &updatedUnix, &m.Kind,
			&m.SourcePipe, &m.Signal, &m.Content, &tagStr,
			&maxStrength,
		); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdUnix, 0)
		m.UpdatedAt = time.Unix(updatedUnix, 0)
		if tagStr != "" {
			m.Tags = strings.Split(tagStr, ",")
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
