package store

import (
	"testing"
)

func TestCreateEdge_Basic(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveInvocation("educate", "teach Go", "answer 1")
	id2, _ := s.SaveInvocation("educate", "teach channels", "answer 2")

	edge := Edge{
		SourceID: id1,
		TargetID: id2,
		Relation: "co_occurred",
		Strength: 1.0,
	}
	if err := s.CreateEdge(edge); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	// Verify via traversal
	connected, err := s.TraverseFrom([]string{id1}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom: %v", err)
	}
	if len(connected) != 1 {
		t.Fatalf("expected 1 connected memory, got %d", len(connected))
	}
	if connected[0].ID != id2 {
		t.Errorf("expected connected ID %s, got %s", id2, connected[0].ID)
	}
}

func TestCreateEdge_UniqueConstraint_StrengthIncrement(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveInvocation("educate", "teach Go", "answer 1")
	id2, _ := s.SaveInvocation("educate", "teach channels", "answer 2")

	edge := Edge{SourceID: id1, TargetID: id2, Relation: "co_occurred", Strength: 1.0}

	// Insert same edge twice
	if err := s.CreateEdge(edge); err != nil {
		t.Fatalf("CreateEdge first: %v", err)
	}
	if err := s.CreateEdge(edge); err != nil {
		t.Fatalf("CreateEdge second: %v", err)
	}

	// Strength should now be 2.0 (1.0 initial + 1 increment)
	var strength float64
	err := s.db.QueryRow(
		"SELECT strength FROM memory_edges WHERE source_id = ? AND target_id = ? AND relation = ?",
		id1, id2, "co_occurred",
	).Scan(&strength)
	if err != nil {
		t.Fatalf("scan strength: %v", err)
	}
	if strength != 2.0 {
		t.Errorf("expected strength=2.0 after duplicate insert, got %f", strength)
	}
}

func TestCreateEdge_SelfReferential_Ignored(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveInvocation("educate", "teach Go", "answer")

	edge := Edge{SourceID: id1, TargetID: id1, Relation: "refined_from"}
	if err := s.CreateEdge(edge); err != nil {
		t.Fatalf("CreateEdge self-ref: %v", err)
	}

	// No edge should have been created
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM memory_edges WHERE source_id = ? AND target_id = ?", id1, id1).Scan(&count)
	if count != 0 {
		t.Errorf("expected no self-referential edge, got %d edges", count)
	}
}

func TestCreateEdges_BatchInsert(t *testing.T) {
	s := tempDB(t)

	ids := make([]string, 4)
	for i := range ids {
		id, _ := s.SaveInvocation("educate", "teach topic", "answer")
		ids[i] = id
	}

	edges := []Edge{
		{SourceID: ids[0], TargetID: ids[1], Relation: "co_occurred"},
		{SourceID: ids[0], TargetID: ids[2], Relation: "co_occurred"},
		{SourceID: ids[1], TargetID: ids[2], Relation: "co_occurred"},
		{SourceID: ids[3], TargetID: ids[0], Relation: "produced_by"},
	}

	if err := s.CreateEdges(edges); err != nil {
		t.Fatalf("CreateEdges: %v", err)
	}

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM memory_edges").Scan(&count)
	if count != 4 {
		t.Errorf("expected 4 edges, got %d", count)
	}
}

func TestCreateEdges_Empty(t *testing.T) {
	s := tempDB(t)
	if err := s.CreateEdges(nil); err != nil {
		t.Fatalf("CreateEdges nil: %v", err)
	}
	if err := s.CreateEdges([]Edge{}); err != nil {
		t.Fatalf("CreateEdges empty: %v", err)
	}
}

func TestTraverseFrom_BothDirections(t *testing.T) {
	s := tempDB(t)

	anchor, _ := s.SaveInvocation("educate", "anchor topic", "anchor answer")
	neighbor1, _ := s.SaveInvocation("educate", "neighbor 1", "answer 1")
	neighbor2, _ := s.SaveInvocation("educate", "neighbor 2", "answer 2")

	// anchor → neighbor1 (anchor is source)
	s.CreateEdge(Edge{SourceID: anchor, TargetID: neighbor1, Relation: "produced_by"})
	// neighbor2 → anchor (anchor is target)
	s.CreateEdge(Edge{SourceID: neighbor2, TargetID: anchor, Relation: "produced_by"})

	connected, err := s.TraverseFrom([]string{anchor}, []string{"produced_by"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom: %v", err)
	}
	if len(connected) != 2 {
		t.Fatalf("expected 2 connected memories (both directions), got %d", len(connected))
	}

	ids := map[string]bool{}
	for _, m := range connected {
		ids[m.ID] = true
	}
	if !ids[neighbor1] {
		t.Error("expected neighbor1 in results (anchor→neighbor1)")
	}
	if !ids[neighbor2] {
		t.Error("expected neighbor2 in results (neighbor2→anchor)")
	}
}

func TestTraverseFrom_Deduplication(t *testing.T) {
	s := tempDB(t)

	anchor, _ := s.SaveInvocation("educate", "anchor topic", "anchor answer")
	neighbor, _ := s.SaveInvocation("educate", "neighbor", "answer")

	// Two different relation types pointing to the same neighbor
	s.CreateEdge(Edge{SourceID: anchor, TargetID: neighbor, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: anchor, TargetID: neighbor, Relation: "produced_by"})

	connected, err := s.TraverseFrom([]string{anchor}, []string{"co_occurred", "produced_by"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom: %v", err)
	}
	if len(connected) != 1 {
		t.Errorf("expected 1 deduplicated result, got %d", len(connected))
	}
}

func TestTraverseFrom_StrengthOrdering(t *testing.T) {
	s := tempDB(t)

	anchor, _ := s.SaveInvocation("educate", "anchor", "anchor answer")
	weak, _ := s.SaveInvocation("educate", "weak", "weak answer")
	strong, _ := s.SaveInvocation("educate", "strong", "strong answer")

	s.CreateEdge(Edge{SourceID: anchor, TargetID: weak, Relation: "co_occurred", Strength: 1.0})
	s.CreateEdge(Edge{SourceID: anchor, TargetID: strong, Relation: "co_occurred", Strength: 5.0})

	connected, err := s.TraverseFrom([]string{anchor}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom: %v", err)
	}
	if len(connected) != 2 {
		t.Fatalf("expected 2 results, got %d", len(connected))
	}
	if connected[0].ID != strong {
		t.Errorf("expected strong memory first (higher strength), got %s", connected[0].ID)
	}
}

func TestTraverseFrom_NoEdges(t *testing.T) {
	s := tempDB(t)

	anchor, _ := s.SaveInvocation("educate", "isolated topic", "isolated answer")

	connected, err := s.TraverseFrom([]string{anchor}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom with no edges: %v", err)
	}
	if len(connected) != 0 {
		t.Errorf("expected 0 results for isolated memory, got %d", len(connected))
	}
}

func TestTraverseFrom_EmptyAnchors(t *testing.T) {
	s := tempDB(t)

	connected, err := s.TraverseFrom(nil, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom nil anchors: %v", err)
	}
	if len(connected) != 0 {
		t.Errorf("expected 0 results for nil anchors, got %d", len(connected))
	}

	connected, err = s.TraverseFrom([]string{}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom empty anchors: %v", err)
	}
	if len(connected) != 0 {
		t.Errorf("expected 0 results for empty anchors, got %d", len(connected))
	}
}

// --- Two-hop traversal tests ---

func TestTraverseFrom_TwoHop(t *testing.T) {
	s := tempDB(t)

	// Chain: A → B → C (via edges)
	idA, _ := s.SaveInvocation("educate", "node A", "answer A")
	idB, _ := s.SaveInvocation("educate", "node B", "answer B")
	idC, _ := s.SaveInvocation("educate", "node C", "answer C")

	s.CreateEdge(Edge{SourceID: idA, TargetID: idB, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idB, TargetID: idC, Relation: "co_occurred"})

	// One-hop from A: only B
	hop1, err := s.TraverseFrom([]string{idA}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom depth=1: %v", err)
	}
	if len(hop1) != 1 || hop1[0].ID != idB {
		t.Errorf("expected only B at depth=1, got %d results", len(hop1))
	}

	// Two-hop from A: B and C
	both, err := s.TraverseFrom([]string{idA}, []string{"co_occurred"}, 10, 2)
	if err != nil {
		t.Fatalf("TraverseFrom depth=2: %v", err)
	}

	ids := map[string]bool{}
	for _, m := range both {
		ids[m.ID] = true
	}
	if !ids[idB] {
		t.Error("expected B in two-hop results")
	}
	if !ids[idC] {
		t.Error("expected C in two-hop results (two hops from A via B)")
	}
}

func TestTraverseFrom_TwoHop_Dedup(t *testing.T) {
	s := tempDB(t)

	// A→B, A→C, B→C: C is reachable in one hop (A→C) and two hops (A→B→C)
	idA, _ := s.SaveInvocation("educate", "node A", "answer A")
	idB, _ := s.SaveInvocation("educate", "node B", "answer B")
	idC, _ := s.SaveInvocation("educate", "node C", "answer C")

	s.CreateEdge(Edge{SourceID: idA, TargetID: idB, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idA, TargetID: idC, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idB, TargetID: idC, Relation: "co_occurred"})

	both, err := s.TraverseFrom([]string{idA}, []string{"co_occurred"}, 10, 2)
	if err != nil {
		t.Fatalf("TraverseFrom depth=2: %v", err)
	}

	// C should appear only once (from first-hop, closer position)
	cCount := 0
	for _, m := range both {
		if m.ID == idC {
			cCount++
		}
	}
	if cCount != 1 {
		t.Errorf("expected C exactly once in results (dedup), got %d times", cCount)
	}
}

func TestTraverseFrom_DefaultDepth(t *testing.T) {
	s := tempDB(t)

	// Chain: A→B→C
	idA, _ := s.SaveInvocation("educate", "node A", "answer A")
	idB, _ := s.SaveInvocation("educate", "node B", "answer B")
	idC, _ := s.SaveInvocation("educate", "node C", "answer C")

	s.CreateEdge(Edge{SourceID: idA, TargetID: idB, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idB, TargetID: idC, Relation: "co_occurred"})

	// No maxDepth arg: default is depth=1, so C should not appear
	results, err := s.TraverseFrom([]string{idA}, []string{"co_occurred"}, 10)
	if err != nil {
		t.Fatalf("TraverseFrom default depth: %v", err)
	}

	for _, m := range results {
		if m.ID == idC {
			t.Error("C should not appear at default depth=1")
		}
	}
}

func TestTraverseFrom_TwoHop_Limit(t *testing.T) {
	s := tempDB(t)

	// A connects to B, C (hop1); B connects to D, E (hop2)
	idA, _ := s.SaveInvocation("educate", "node A", "answer A")
	idB, _ := s.SaveInvocation("educate", "node B", "answer B")
	idC, _ := s.SaveInvocation("educate", "node C", "answer C")
	idD, _ := s.SaveInvocation("educate", "node D", "answer D")
	idE, _ := s.SaveInvocation("educate", "node E", "answer E")

	s.CreateEdge(Edge{SourceID: idA, TargetID: idB, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idA, TargetID: idC, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idB, TargetID: idD, Relation: "co_occurred"})
	s.CreateEdge(Edge{SourceID: idB, TargetID: idE, Relation: "co_occurred"})

	// Limit=3: should return at most 3 results across both hops
	results, err := s.TraverseFrom([]string{idA}, []string{"co_occurred"}, 3, 2)
	if err != nil {
		t.Fatalf("TraverseFrom limit test: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results with limit=3, got %d", len(results))
	}
}

func TestOnDeleteCascade(t *testing.T) {
	s := tempDB(t)

	id1, _ := s.SaveInvocation("educate", "teach Go", "answer 1")
	id2, _ := s.SaveInvocation("educate", "teach channels", "answer 2")

	s.CreateEdge(Edge{SourceID: id1, TargetID: id2, Relation: "co_occurred"})

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM memory_edges").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 edge before delete, got %d", count)
	}

	// Delete the source memory — cascade should remove the edge
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id1)
	if err != nil {
		t.Fatalf("delete memory: %v", err)
	}

	s.db.QueryRow("SELECT COUNT(*) FROM memory_edges").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 edges after cascade delete, got %d", count)
	}
}
