-- +goose Up
ALTER TABLE memories ADD COLUMN confidence REAL NOT NULL DEFAULT 0.5;
ALTER TABLE memories ADD COLUMN valid_until INTEGER;

-- Set confidence defaults based on existing kind values
UPDATE memories SET confidence = 0.9 WHERE kind = 'explicit';
UPDATE memories SET confidence = 0.7 WHERE kind = 'working_state';
UPDATE memories SET confidence = 0.5 WHERE kind = 'invocation';

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0; goose down is best-effort.
