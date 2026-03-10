-- +goose Up
INSERT INTO memories (id, created_at, updated_at, kind, source_pipe, content, tags, confidence, data)
SELECT
    id,
    created_at,
    COALESCE(completed_at, created_at),
    'todo',
    'todo',
    title,
    tags,
    0.8,
    json_object(
        'status', status,
        'priority', priority,
        'due_date', COALESCE(due_date, ''),
        'external_id', COALESCE(external_id, ''),
        'details', COALESCE(details, '')
    )
FROM todos
WHERE id NOT IN (SELECT id FROM memories);

-- Preserve memory_id edges: if a todo had a memory_id linking to its creation invocation,
-- create a produced_by edge from the todo memory to that invocation memory.
INSERT OR IGNORE INTO memory_edges (id, source_id, target_id, relation, strength, created_at)
SELECT
    lower(hex(randomblob(16))),
    t.id,
    t.memory_id,
    'produced_by',
    1.0,
    t.created_at
FROM todos t
WHERE t.memory_id IS NOT NULL AND t.memory_id != ''
  AND t.id IN (SELECT id FROM memories)
  AND t.memory_id IN (SELECT id FROM memories);

-- +goose Down
DELETE FROM memories WHERE kind = 'todo' AND source_pipe = 'todo';
