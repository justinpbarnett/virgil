package store

import (
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationNoTx(up00005, down00005)
}

func up00005(db *sql.DB) error {
	if err := addColumnIfMissing(db, "memories", "confidence", "REAL NOT NULL DEFAULT 0.5"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "memories", "valid_until", "INTEGER"); err != nil {
		return err
	}

	// Set confidence defaults based on existing kind values (only update rows
	// still at the default so this is safe to re-run).
	for _, q := range []string{
		"UPDATE memories SET confidence = 0.9 WHERE kind = 'explicit' AND confidence = 0.5",
		"UPDATE memories SET confidence = 0.7 WHERE kind = 'working_state' AND confidence = 0.5",
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func down00005(db *sql.DB) error {
	return nil // SQLite < 3.35.0 doesn't support DROP COLUMN; best-effort no-op.
}

func addColumnIfMissing(db *sql.DB, table, column, typedef string) error {
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('"+table+"') WHERE name = ?", column,
	).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + typedef)
	return err
}
