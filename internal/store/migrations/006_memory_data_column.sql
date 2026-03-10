-- +goose Up
ALTER TABLE memories ADD COLUMN data TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; no-op.
