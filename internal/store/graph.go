package store

import (
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
	defer tx.Rollback() //nolint:errcheck

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

// TraverseFrom performs a graph traversal from the given anchor memory IDs.
// By default (no maxDepth arg, or maxDepth=1) it performs a one-hop traversal.
// Pass maxDepth=2 to also retrieve second-hop neighbors.
// Returns connected memories (not the anchors themselves), deduplicated and
// ordered hop-first (first-hop results before second-hop results), then by
// edge strength descending within each hop. The combined limit applies across
// both hops.
func (s *Store) TraverseFrom(anchorIDs []string, relations []string, limit int, maxDepth ...int) ([]Memory, error) {
	if len(anchorIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	depth := 1
	if len(maxDepth) > 0 && maxDepth[0] >= 2 {
		depth = 2
	}

	now := time.Now().UnixNano()

	// First hop: traverse from original anchors, excluding anchors from results.
	hop1, err := s.traverseHop(anchorIDs, anchorIDs, relations, limit, now)
	if err != nil {
		return nil, err
	}

	if depth < 2 || len(hop1) == 0 {
		return hop1, nil
	}

	// Second hop: traverse from first-hop IDs, excluding both original anchors
	// and first-hop IDs from results.
	hop1IDs := make([]string, len(hop1))
	for i, m := range hop1 {
		hop1IDs[i] = m.ID
	}

	excludeIDs := make([]string, 0, len(anchorIDs)+len(hop1IDs))
	excludeIDs = append(excludeIDs, anchorIDs...)
	excludeIDs = append(excludeIDs, hop1IDs...)

	hop2, err := s.traverseHop(hop1IDs, excludeIDs, relations, limit, now)
	if err != nil {
		return nil, err
	}

	// Combine hop1 and hop2, then apply limit across both.
	result := append(hop1, hop2...)
	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// TraverseHops performs a two-hop traversal and returns results split by hop.
// hop1 contains first-hop neighbors; hop2 contains second-hop neighbors.
// Both are deduplicated and ordered by edge strength within each hop.
func (s *Store) TraverseHops(anchorIDs []string, relations []string, limit int) (hop1, hop2 []Memory, err error) {
	if len(anchorIDs) == 0 {
		return nil, nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	now := time.Now().UnixNano()

	hop1, err = s.traverseHop(anchorIDs, anchorIDs, relations, limit, now)
	if err != nil || len(hop1) == 0 {
		return hop1, nil, err
	}

	hop1IDs := make([]string, len(hop1))
	for i, m := range hop1 {
		hop1IDs[i] = m.ID
	}

	excludeIDs := make([]string, 0, len(anchorIDs)+len(hop1IDs))
	excludeIDs = append(excludeIDs, anchorIDs...)
	excludeIDs = append(excludeIDs, hop1IDs...)

	hop2, err = s.traverseHop(hop1IDs, excludeIDs, relations, limit, now)
	if err != nil {
		return hop1, nil, err
	}

	// Apply combined limit.
	total := len(hop1) + len(hop2)
	if total > limit {
		remaining := limit - len(hop1)
		if remaining <= 0 {
			return hop1[:limit], nil, nil
		}
		hop2 = hop2[:remaining]
	}

	return hop1, hop2, nil
}

// traverseHop executes a single-hop graph query from anchorIDs, excluding
// excludeIDs from the result set. Filters expired memories via now (UnixNano).
func (s *Store) traverseHop(anchorIDs []string, excludeIDs []string, relations []string, limit int, now int64) ([]Memory, error) {
	anchorPlaceholders := placeholders(len(anchorIDs))
	excludePlaceholders := placeholders(len(excludeIDs))

	relationFilter := ""
	if len(relations) > 0 {
		relationFilter = fmt.Sprintf("AND e.relation IN (%s)", placeholders(len(relations)))
	}

	// Arg order must match SQL placeholder positions:
	// 1. source_id IN (anchorIDs)
	// 2. target_id IN (anchorIDs)
	// 3. m.id NOT IN (excludeIDs)
	// 4. valid_until > now
	// 5. e.relation IN (relations)  [if any]
	// 6. LIMIT
	args := make([]any, 0)
	for _, id := range anchorIDs {
		args = append(args, id)
	}
	for _, id := range anchorIDs {
		args = append(args, id)
	}
	for _, id := range excludeIDs {
		args = append(args, id)
	}
	args = append(args, now)
	for _, r := range relations {
		args = append(args, r)
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT m.id, m.created_at, m.updated_at, m.kind,
		       COALESCE(m.source_pipe, ''), COALESCE(m.signal, ''), m.content, COALESCE(m.tags, ''),
		       m.confidence, COALESCE(m.data, ''),
		       MAX(e.strength) as max_strength
		FROM memory_edges e
		JOIN memories m ON (
		    (e.source_id IN (%s) AND e.target_id = m.id)
		    OR
		    (e.target_id IN (%s) AND e.source_id = m.id)
		)
		WHERE m.id NOT IN (%s)
		  AND (m.valid_until IS NULL OR m.valid_until > ?)
		%s
		GROUP BY m.id
		ORDER BY max_strength DESC
		LIMIT ?
	`, anchorPlaceholders, anchorPlaceholders, excludePlaceholders, relationFilter)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var createdNano, updatedNano int64
		var tagStr string
		var maxStrength float64
		if err := rows.Scan(
			&m.ID, &createdNano, &updatedNano, &m.Kind,
			&m.SourcePipe, &m.Signal, &m.Content, &tagStr,
			&m.Confidence, &m.Data,
			&maxStrength,
		); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(0, createdNano)
		m.UpdatedAt = time.Unix(0, updatedNano)
		if tagStr != "" {
			m.Tags = strings.Split(tagStr, ",")
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
