-- +goose Up
CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 3,
    due_date TEXT,
    tags TEXT NOT NULL DEFAULT '',
    pipe_affinity TEXT NOT NULL DEFAULT '',
    memory_id TEXT,
    created_at INTEGER NOT NULL,
    completed_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status);
CREATE INDEX IF NOT EXISTS idx_todos_priority ON todos(priority);
CREATE INDEX IF NOT EXISTS idx_todos_status_priority ON todos(status, priority);

-- +goose Down
DROP TABLE IF EXISTS todos;
