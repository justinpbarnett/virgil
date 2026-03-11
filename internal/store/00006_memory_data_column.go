package store

import (
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTx(up00006, down00006)
}

func up00006(db *sql.DB) error {
	return addColumnIfMissing(db, "memories", "data", "TEXT")
}

func down00006(db *sql.DB) error {
	return nil // SQLite < 3.35.0 doesn't support DROP COLUMN; best-effort no-op.
}
