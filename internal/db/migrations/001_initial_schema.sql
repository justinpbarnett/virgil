-- +goose Up

---------------------------------------------------------------------------
-- Memory
---------------------------------------------------------------------------

CREATE TABLE memory (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL CHECK(type IN ('observation', 'interaction', 'fact')),
    scope       TEXT NOT NULL DEFAULT 'personal',
    topic       TEXT,
    content     TEXT NOT NULL,
    source      TEXT,
    embedding   BLOB,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at  TEXT,
    summary_of  TEXT
);

CREATE INDEX idx_memory_type ON memory(type);
CREATE INDEX idx_memory_scope ON memory(scope);
CREATE INDEX idx_memory_topic ON memory(topic);
CREATE INDEX idx_memory_created ON memory(created_at);
CREATE INDEX idx_memory_source ON memory(source);

CREATE VIRTUAL TABLE memory_fts USING fts5(
    content,
    topic,
    content=memory,
    content_rowid=rowid,
    tokenize='porter unicode61'
);

-- +goose StatementBegin
CREATE TRIGGER memory_ai AFTER INSERT ON memory BEGIN
    INSERT INTO memory_fts(rowid, content, topic)
    VALUES (new.rowid, new.content, new.topic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_ad AFTER DELETE ON memory BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, content, topic)
    VALUES ('delete', old.rowid, old.content, old.topic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER memory_au AFTER UPDATE ON memory BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, content, topic)
    VALUES ('delete', old.rowid, old.content, old.topic);
    INSERT INTO memory_fts(rowid, content, topic)
    VALUES (new.rowid, new.content, new.topic);
END;
-- +goose StatementEnd

CREATE TABLE memory_entities (
    memory_id   TEXT NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    entity      TEXT NOT NULL,
    entity_type TEXT,
    role        TEXT,
    PRIMARY KEY (memory_id, entity, role)
);

CREATE INDEX idx_entities_entity ON memory_entities(entity);

CREATE TABLE fact_history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    fact_id     TEXT NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    old_content TEXT,
    new_content TEXT,
    reason      TEXT,
    changed_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

---------------------------------------------------------------------------
-- Events
---------------------------------------------------------------------------

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    component   TEXT NOT NULL,
    action      TEXT NOT NULL,
    input       TEXT,
    output      TEXT,
    duration_ms INTEGER,
    error       TEXT,
    trace_id    TEXT,
    span_id     TEXT,
    parent_span TEXT,
    model       TEXT,
    tokens_in   INTEGER,
    tokens_out  INTEGER
);

CREATE INDEX idx_events_trace ON events(trace_id);
CREATE INDEX idx_events_component ON events(component);
CREATE INDEX idx_events_timestamp ON events(timestamp);
CREATE INDEX idx_events_error ON events(error) WHERE error IS NOT NULL;

---------------------------------------------------------------------------
-- Tasks
---------------------------------------------------------------------------

CREATE TABLE tasks (
    id          TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'done', 'dropped')),
    priority    TEXT DEFAULT 'normal' CHECK(priority IN ('urgent', 'high', 'normal', 'low')),
    source      TEXT,
    source_id   TEXT,
    bridge      TEXT,
    due_at      TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    completed_at TEXT,
    metadata    TEXT
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_source ON tasks(source, source_id);

---------------------------------------------------------------------------
-- Sync state
---------------------------------------------------------------------------

CREATE TABLE sync_state (
    source      TEXT PRIMARY KEY,
    last_id     TEXT,
    last_at     TEXT,
    metadata    TEXT
);

---------------------------------------------------------------------------
-- Trust
---------------------------------------------------------------------------

CREATE TABLE trust_scores (
    action_type TEXT NOT NULL,
    channel     TEXT NOT NULL,
    contact     TEXT NOT NULL DEFAULT '*',
    approvals   INTEGER NOT NULL DEFAULT 0,
    rejections  INTEGER NOT NULL DEFAULT 0,
    edits       INTEGER NOT NULL DEFAULT 0,
    auto_approved BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (action_type, channel, contact)
);

-- +goose Down

DROP TABLE IF EXISTS trust_scores;
DROP TABLE IF EXISTS sync_state;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS fact_history;
DROP TABLE IF EXISTS memory_entities;
DROP TRIGGER IF EXISTS memory_au;
DROP TRIGGER IF EXISTS memory_ad;
DROP TRIGGER IF EXISTS memory_ai;
DROP TABLE IF EXISTS memory_fts;
DROP TABLE IF EXISTS memory;
