-- +goose Up
CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    kind TEXT NOT NULL,
    source_pipe TEXT,
    signal TEXT,
    content TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '',
    embedding BLOB
);

CREATE INDEX IF NOT EXISTS idx_memories_kind ON memories(kind);
CREATE INDEX IF NOT EXISTS idx_memories_kind_id ON memories(kind, id);
CREATE INDEX IF NOT EXISTS idx_memories_source_pipe ON memories(source_pipe);
CREATE INDEX IF NOT EXISTS idx_memories_created_at ON memories(created_at);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    signal,
    content='memories',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, signal) VALUES (new.rowid, new.content, COALESCE(new.signal, ''));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, signal) VALUES('delete', old.rowid, old.content, COALESCE(old.signal, ''));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, signal) VALUES('delete', old.rowid, old.content, COALESCE(old.signal, ''));
    INSERT INTO memories_fts(rowid, content, signal) VALUES (new.rowid, new.content, COALESCE(new.signal, ''));
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS memories_au;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS memories_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS memories_ai;
-- +goose StatementEnd
DROP TABLE IF EXISTS memories_fts;
DROP TABLE IF EXISTS memories;
