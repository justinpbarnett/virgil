package trust

import (
	"database/sql"
	"fmt"
	"time"
)

// Store reads and writes trust scores backed by the trust_scores table.
type Store struct {
	db        *sql.DB
	threshold int
}

// Score is a row from the trust_scores table.
type Score struct {
	ActionType string `json:"action_type"`
	Channel    string `json:"channel"`
	Contact    string `json:"contact"`
	Approvals  int    `json:"approvals"`
	Rejections int    `json:"rejections"`
	UpdatedAt  string `json:"updated_at"`
}

// Value returns approvals minus rejections.
func (s Score) Value() int { return s.Approvals - s.Rejections }

// NewStore creates a Store backed by the given database and threshold.
// If threshold <= 0 it defaults to 15.
func NewStore(db *sql.DB, threshold int) *Store {
	if threshold <= 0 {
		threshold = 15
	}
	return &Store{db: db, threshold: threshold}
}

// Threshold returns the auto-approve threshold.
func (s *Store) Threshold() int { return s.threshold }

// AutoApproves returns true if the (actionType, channel, contact) tuple has a
// trust score at or above the threshold. Checks the exact tuple first, then
// (actionType, channel, "*"), then (actionType, "*", "*").
func (s *Store) AutoApproves(actionType, channel, contact string) (bool, error) {
	var value int
	err := s.db.QueryRow(`
		SELECT approvals - rejections
		FROM trust_scores
		WHERE action_type = ?
		  AND (
		      (channel = ? AND contact = ?)
		   OR (channel = ? AND contact = '*')
		   OR (channel = '*' AND contact = '*')
		  )
		ORDER BY
		  (channel != '*') DESC,
		  (contact != '*') DESC
		LIMIT 1`,
		actionType, channel, contact, channel).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("trust check: %w", err)
	}
	return value >= s.threshold, nil
}

// Record increments the approval (approved=true) or rejection count for the
// given action tuple. Inserts a row if none exists.
func (s *Store) Record(actionType, channel, contact string, approved bool) error {
	channel = normalizeWildcard(channel)
	contact = normalizeWildcard(contact)
	now := time.Now().UTC().Format(time.RFC3339)
	col := "rejections"
	if approved {
		col = "approvals"
	}
	_, err := s.db.Exec(fmt.Sprintf(`
		INSERT INTO trust_scores (action_type, channel, contact, %s, updated_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(action_type, channel, contact) DO UPDATE SET
			%s = %s + 1,
			updated_at = excluded.updated_at`, col, col, col),
		actionType, channel, contact, now)
	return err
}

// Rollback decrements approvals for the given tuple, flooring at 0.
func (s *Store) Rollback(actionType, channel, contact string) error {
	channel = normalizeWildcard(channel)
	contact = normalizeWildcard(contact)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE trust_scores
		SET approvals = MAX(0, approvals - 1), updated_at = ?
		WHERE action_type = ? AND channel = ? AND contact = ?`,
		now, actionType, channel, contact)
	return err
}

// List returns all trust scores ordered by action_type, channel, contact.
func (s *Store) List() ([]Score, error) {
	rows, err := s.db.Query(`
		SELECT action_type, channel, contact, approvals, rejections, updated_at
		FROM trust_scores
		ORDER BY action_type, channel, contact`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	scores := make([]Score, 0)
	for rows.Next() {
		var sc Score
		if err := rows.Scan(&sc.ActionType, &sc.Channel, &sc.Contact,
			&sc.Approvals, &sc.Rejections, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		scores = append(scores, sc)
	}
	return scores, rows.Err()
}

func normalizeWildcard(s string) string {
	if s == "" {
		return "*"
	}
	return s
}
