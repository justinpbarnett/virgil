package trust

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE trust_scores (
		action_type TEXT NOT NULL,
		channel     TEXT NOT NULL,
		contact     TEXT NOT NULL DEFAULT '*',
		approvals   INTEGER NOT NULL DEFAULT 0,
		rejections  INTEGER NOT NULL DEFAULT 0,
		updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
		PRIMARY KEY (action_type, channel, contact)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRollback(t *testing.T) {
	db := newTestDB(t)
	s := NewStore(db, 3)

	ap := AutoApproval{actionType: "email_send", channel: "gmail", contact: "alice"}

	if err := s.Record("email_send", "gmail", "alice", true); err != nil {
		t.Fatal(err)
	}

	if err := s.Rollback(ap); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	scores, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 || scores[0].Approvals != 0 {
		t.Fatalf("expected approvals=0 after rollback, got %+v", scores)
	}
}

func TestRollbackFloor(t *testing.T) {
	db := newTestDB(t)
	s := NewStore(db, 3)

	if err := s.Record("email_send", "*", "*", true); err != nil {
		t.Fatal(err)
	}

	ap := AutoApproval{actionType: "email_send", channel: "*", contact: "*"}
	for range 5 {
		if err := s.Rollback(ap); err != nil {
			t.Fatalf("Rollback failed: %v", err)
		}
	}

	scores, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if scores[0].Approvals != 0 {
		t.Fatalf("expected approvals floored at 0, got %d", scores[0].Approvals)
	}
}

func TestRollbackMissingRow(t *testing.T) {
	db := newTestDB(t)
	s := NewStore(db, 3)

	ap := AutoApproval{actionType: "email_send", channel: "gmail", contact: "alice"}
	err := s.Rollback(ap)
	if err == nil {
		t.Fatal("expected error for missing row, got nil")
	}
}

func TestListParsesUpdatedAt(t *testing.T) {
	db := newTestDB(t)
	s := NewStore(db, 3)

	if err := s.Record("ping", "*", "*", true); err != nil {
		t.Fatal(err)
	}

	scores, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores[0].UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should not be zero after Record")
	}
}
