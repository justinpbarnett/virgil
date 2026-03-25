package memory

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
)

// SearchResult is a memory entry with a relevance score.
type SearchResult struct {
	Entry
	Score float64 `json:"score"`
}

// SearchParams controls search behavior.
type SearchParams struct {
	Query string
	Type  string // filter by memory type
	Scope string // filter by scope
	Limit int
}

// Search performs hybrid FTS5 + entity graph search with RRF merge.
func (s *Store) Search(p SearchParams) ([]SearchResult, error) {
	if strings.TrimSpace(p.Query) == "" {
		return nil, fmt.Errorf("search query must not be empty")
	}
	if p.Limit <= 0 {
		p.Limit = 10
	}

	ftsResults, err := s.ftsSearch(p)
	if err != nil {
		return nil, err
	}

	entityResults, err := s.entitySearch(p)
	if err != nil {
		return nil, err
	}

	merged := rrfMerge(ftsResults, entityResults)

	if len(merged) > p.Limit {
		merged = merged[:p.Limit]
	}

	results := make([]SearchResult, 0, len(merged))
	for _, sc := range merged {
		entry, err := s.Get(sc.id)
		if err != nil {
			return nil, fmt.Errorf("hydrate memory %s: %w", sc.id, err)
		}
		if entry == nil {
			continue
		}
		results = append(results, SearchResult{Entry: *entry, Score: sc.score})
	}

	return results, nil
}

type scoredID struct {
	id    string
	score float64
}

func (s *Store) ftsSearch(p SearchParams) ([]scoredID, error) {
	q := `
		SELECT m.id, rank
		FROM memory m
		JOIN memory_fts ON memory_fts.rowid = m.rowid
		WHERE memory_fts MATCH ?`
	args := []any{ftsEscape(p.Query)}

	if p.Type != "" {
		q += " AND m.type = ?"
		args = append(args, p.Type)
	}
	if p.Scope != "" {
		q += " AND m.scope = ?"
		args = append(args, p.Scope)
	}

	// FTS5 rank is negative (lower = better), grab a generous candidate set
	q += " ORDER BY rank LIMIT 100"

	rows, err := s.database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []scoredID
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, err
		}
		results = append(results, scoredID{id: id, score: -rank})
	}
	return results, rows.Err()
}

func (s *Store) entitySearch(p SearchParams) ([]scoredID, error) {
	q := `
		SELECT DISTINCT m.id
		FROM memory m
		JOIN memory_entities me ON m.id = me.memory_id
		WHERE me.entity LIKE ?`
	args := []any{"%" + p.Query + "%"}

	if p.Type != "" {
		q += " AND m.type = ?"
		args = append(args, p.Type)
	}
	if p.Scope != "" {
		q += " AND m.scope = ?"
		args = append(args, p.Scope)
	}
	q += " LIMIT 50"

	rows, err := s.database.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []scoredID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		results = append(results, scoredID{id: id, score: 1.0})
	}
	return results, rows.Err()
}

// EntityTraverse returns memories connected to an entity within one hop.
func (s *Store) EntityTraverse(entity string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.queryEntries(`
		SELECT DISTINCT m.id, m.type, m.scope, m.topic, m.content, m.source, m.created_at, m.expires_at
		FROM memory m
		JOIN memory_entities me ON m.id = me.memory_id
		WHERE me.entity LIKE ?
		ORDER BY m.created_at DESC
		LIMIT ?`, "%"+entity+"%", limit)
}

// SearchRecent returns the most recent memories, optionally filtered by topic and type.
func (s *Store) SearchRecent(topic string, memType string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT id, type, scope, topic, content, source, created_at, expires_at
		FROM memory WHERE 1=1`
	var args []any
	if topic != "" {
		q += " AND (topic LIKE ? OR content LIKE ?)"
		args = append(args, "%"+topic+"%", "%"+topic+"%")
	}
	if memType != "" {
		q += " AND type = ?"
		args = append(args, memType)
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	return s.queryEntries(q, args...)
}

// ftsEscape quotes each term for safe use in FTS5 MATCH expressions.
// Characters like ", *, -, (, ) have special meaning in FTS5 syntax.
func ftsEscape(query string) string {
	terms := strings.Fields(query)
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(terms, " ")
}

// rrfMerge combines multiple ranked lists using Reciprocal Rank Fusion.
// k=60 is standard. Each list contributes 1/(k+rank) per item.
func rrfMerge(lists ...[]scoredID) []scoredID {
	const k = 60.0
	scores := make(map[string]float64)

	for _, list := range lists {
		if list == nil {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
		for rank, item := range list {
			scores[item.id] += 1.0 / (k + float64(rank+1))
		}
	}

	result := make([]scoredID, 0, len(scores))
	for id, score := range scores {
		result = append(result, scoredID{id: id, score: score})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].score > result[j].score })
	return result
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Called by vectorRescore once the bridge provides embeddings.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// decodeEmbedding converts a BLOB of little-endian float32s to a slice.
// Called by vectorRescore once the bridge provides embeddings.
func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	n := len(b) / 4
	result := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(b[i*4 : (i+1)*4])
		result[i] = math.Float32frombits(bits)
	}
	return result
}
