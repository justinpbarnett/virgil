#!/usr/bin/env bash
# test/skeleton.sh -- Stage 1 acceptance tests
# Verifies: init creates DB with all tables, status returns valid JSON,
# events table accepts and retrieves writes.
set -euo pipefail

VIRGIL="./virgil"
TEST_DIR=$(mktemp -d)
TEST_CONFIG="$TEST_DIR/virgil.yaml"
TEST_DB="$TEST_DIR/data.db"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

echo "=== skeleton tests ==="

# ---------- init ----------

echo "--- init ---"

# Write a test config pointing to temp dir
cat > "$TEST_CONFIG" <<EOF
guide:
  name: Test Guide
  data_dir: $TEST_DIR
ai:
  default: anthropic
skills:
  dir: skills/
trust:
  auto_approve_threshold: 15
  default_action: ask
server:
  host: 0.0.0.0
  port: 8080
EOF

$VIRGIL init --config "$TEST_CONFIG" 2>/dev/null
[ -f "$TEST_DB" ] || fail "data.db not created"
pass "data.db created"

# Check all expected tables exist
EXPECTED_TABLES="memory memory_entities fact_history events tasks sync_state trust_scores"
for table in $EXPECTED_TABLES; do
    COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='$table'")
    [ "$COUNT" = "1" ] || fail "table $table not found"
done
pass "all tables created"

# Check FTS5 virtual table
FTS_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_fts'")
[ "$FTS_COUNT" = "1" ] || fail "memory_fts virtual table not found"
pass "FTS5 virtual table created"

# Check triggers
TRIGGER_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM sqlite_master WHERE type='trigger'")
[ "$TRIGGER_COUNT" = "3" ] || fail "expected 3 triggers, got $TRIGGER_COUNT"
pass "3 FTS triggers created"

# Check indexes
INDEX_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%'")
[ "$INDEX_COUNT" -ge 10 ] || fail "expected at least 10 indexes, got $INDEX_COUNT"
pass "indexes created ($INDEX_COUNT)"

# ---------- status ----------

echo "--- status ---"

STATUS=$($VIRGIL status --config "$TEST_CONFIG" 2>/dev/null)
echo "$STATUS" | jq -e '.ok == true' > /dev/null || fail "status.ok not true"
echo "$STATUS" | jq -e '.tables > 0' > /dev/null || fail "status.tables is 0"
echo "$STATUS" | jq -e '.data_dir' > /dev/null || fail "status.data_dir missing"
pass "status returns valid JSON with expected fields"

# ---------- events ----------

echo "--- events ---"

# Insert an event directly via SQL
sqlite3 "$TEST_DB" "INSERT INTO events (component, action, trace_id, span_id) VALUES ('test', 'test_action', 'trace123', 'span456')"

EVENTS=$($VIRGIL events --config "$TEST_CONFIG" --trace trace123 2>/dev/null)
echo "$EVENTS" | jq -e 'length == 1' > /dev/null || fail "expected 1 event"
echo "$EVENTS" | jq -e '.[0].component == "test"' > /dev/null || fail "component mismatch"
echo "$EVENTS" | jq -e '.[0].trace_id == "trace123"' > /dev/null || fail "trace_id mismatch"
pass "events query with --trace filter works"

# Insert more events and test limit
sqlite3 "$TEST_DB" "INSERT INTO events (component, action) VALUES ('test2', 'action2')"
sqlite3 "$TEST_DB" "INSERT INTO events (component, action) VALUES ('test3', 'action3')"

ALL_EVENTS=$($VIRGIL events --config "$TEST_CONFIG" --limit 2 2>/dev/null)
echo "$ALL_EVENTS" | jq -e 'length == 2' > /dev/null || fail "limit not respected"
pass "events --limit works"

COMP_EVENTS=$($VIRGIL events --config "$TEST_CONFIG" --component test2 2>/dev/null)
echo "$COMP_EVENTS" | jq -e 'length == 1' > /dev/null || fail "component filter failed"
pass "events --component filter works"

echo ""
echo "=== all skeleton tests passed ==="
