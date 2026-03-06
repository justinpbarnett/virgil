-- +goose Up
ALTER TABLE todos ADD COLUMN external_id TEXT;
ALTER TABLE todos ADD COLUMN details TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_external_id ON todos(external_id);

-- +goose Down
DROP INDEX IF EXISTS idx_todos_external_id;
ALTER TABLE todos DROP COLUMN details;
ALTER TABLE todos DROP COLUMN external_id;

