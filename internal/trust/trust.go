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
	candidates := []struct{ ch, co string }{
		{channel, contact},
		{channel, "*"},
		{"*", "*"},
	}
	for _, c := range candidates {
		sc, err := s.get(actionType, c.ch, c.co)
		if err != nil {
			return false, err
		}
		if sc != nil && sc.Value() >= s.threshold {
			return true, nil
		}
	}
	return false, nil
}

// Record increments the approval (approved=true) or rejection count for the
// given action tuple. Inserts a row if none exists.
func (s *Store) Record(actionType, channel, contact string, approved bool) error {
	if channel == "" {
		channel = "*"
	}
	if contact == "" {
		contact = "*"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if approved {
		_, err := s.db.Exec(`
			INSERT INTO trust_scores (action_type, channel, contact, approvals, updated_at)
			VALUES (?, ?, ?, 1, ?)
			ON CONFLICT(action_type, channel, contact) DO UPDATE SET
				approvals  = approvals + 1,
				updated_at = excluded.updated_at`,
			actionType, channel, contact, now)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO trust_scores (action_type, channel, contact, rejections, updated_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(action_type, channel, contact) DO UPDATE SET
			rejections = rejections + 1,
			updated_at = excluded.updated_at`,
		actionType, channel, contact, now)
	return err
}

// Rollback decrements approvals for the given tuple, flooring at 0.
func (s *Store) Rollback(actionType, channel, contact string) error {
	if channel == "" {
		channel = "*"
	}
	if contact == "" {
		contact = "*"
	}
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

func (s *Store) get(actionType, channel, contact string) (*Score, error) {
	var sc Score
	err := s.db.QueryRow(`
		SELECT action_type, channel, contact, approvals, rejections, updated_at
		FROM trust_scores
		WHERE action_type = ? AND channel = ? AND contact = ?`,
		actionType, channel, contact).Scan(
		&sc.ActionType, &sc.Channel, &sc.Contact,
		&sc.Approvals, &sc.Rejections, &sc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get trust score: %w", err)
	}
	return &sc, nil
}
