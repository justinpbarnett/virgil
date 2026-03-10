-- +goose Up
DROP TABLE IF EXISTS todos;

-- +goose Down
CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 3,
    due_date TEXT,
    tags TEXT NOT NULL DEFAULT '',
    pipe_affinity TEXT NOT NULL DEFAULT '',
    memory_id TEXT,
    external_id TEXT UNIQUE,
    details TEXT,
    created_at INTEGER NOT NULL,
    completed_at INTEGER
);
