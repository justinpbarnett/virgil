#!/usr/bin/env bash
# test/memory.sh -- Stage 2 acceptance tests
# Verifies: memory store/search/facts, scope isolation, FTS5 triggers, entity graph
set -euo pipefail

VIRGIL="./virgil"
TEST_DIR=$(mktemp -d)
TEST_CONFIG="$TEST_DIR/virgil.yaml"
TEST_DB="$TEST_DIR/data.db"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

# Write test config
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

# Init database
$VIRGIL init --config "$TEST_CONFIG" 2>/dev/null

echo "=== memory tests ==="

# ---------- store ----------

echo "--- store ---"

# Store an observation
OBS=$($VIRGIL memory store observation "Justin had a meeting with Sarah about Q2 planning" \
    --topic "Q2 planning" --config "$TEST_CONFIG" 2>/dev/null)
OBS_ID=$(echo "$OBS" | jq -r '.id')
[ -n "$OBS_ID" ] && [ "$OBS_ID" != "null" ] || fail "observation not stored"
echo "$OBS" | jq -e '.type == "observation"' > /dev/null || fail "observation type wrong"
echo "$OBS" | jq -e '.scope == "personal"' > /dev/null || fail "observation scope wrong"
pass "store observation"

# Store an interaction
INT=$($VIRGIL memory store interaction "User asked about project deadlines" \
    --config "$TEST_CONFIG" 2>/dev/null)
INT_ID=$(echo "$INT" | jq -r '.id')
[ -n "$INT_ID" ] && [ "$INT_ID" != "null" ] || fail "interaction not stored"
pass "store interaction"

# Store a fact
FACT=$($VIRGIL memory store fact "Sarah prefers morning meetings" \
    --topic "meeting preferences" \
    --entities "Sarah:person:subject" \
    --config "$TEST_CONFIG" 2>/dev/null)
FACT_ID=$(echo "$FACT" | jq -r '.id')
[ -n "$FACT_ID" ] && [ "$FACT_ID" != "null" ] || fail "fact not stored"
echo "$FACT" | jq -e '.type == "fact"' > /dev/null || fail "fact type wrong"
pass "store fact with entity"

# Store a fact with scope
SCOPED=$($VIRGIL memory store fact "Enver uses Unity for VR" \
    --topic "tech stack" --scope "bridge:enver" \
    --config "$TEST_CONFIG" 2>/dev/null)
echo "$SCOPED" | jq -e '.scope == "bridge:enver"' > /dev/null || fail "scoped fact scope wrong"
pass "store scoped fact"

# ---------- FTS5 triggers ----------

echo "--- FTS5 triggers ---"

FTS_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM memory_fts")
MEMORY_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM memory")
[ "$FTS_COUNT" = "$MEMORY_COUNT" ] || fail "FTS5 count ($FTS_COUNT) != memory count ($MEMORY_COUNT)"
pass "FTS5 triggers synced ($FTS_COUNT entries)"

# ---------- search ----------

echo "--- search ---"

# FTS5 search
RESULTS=$($VIRGIL memory search "meeting Sarah" --config "$TEST_CONFIG" 2>/dev/null)
RESULT_COUNT=$(echo "$RESULTS" | jq 'length')
[ "$RESULT_COUNT" -ge 1 ] || fail "search returned 0 results"
pass "FTS5 search returns results ($RESULT_COUNT)"

# Search with type filter
TYPE_RESULTS=$($VIRGIL memory search "meeting" --type observation --config "$TEST_CONFIG" 2>/dev/null)
for row in $(echo "$TYPE_RESULTS" | jq -r '.[].type'); do
    [ "$row" = "observation" ] || fail "type filter returned non-observation: $row"
done
pass "search with --type filter"

# Search with limit
LIMITED=$($VIRGIL memory search "meeting" --limit 1 --config "$TEST_CONFIG" 2>/dev/null)
LIMITED_COUNT=$(echo "$LIMITED" | jq 'length')
[ "$LIMITED_COUNT" -le 1 ] || fail "limit not respected, got $LIMITED_COUNT"
pass "search with --limit"

# ---------- scope isolation ----------

echo "--- scope isolation ---"

# Search with scope filter should only return matching scope
PERSONAL=$($VIRGIL memory search "meeting" --scope personal --config "$TEST_CONFIG" 2>/dev/null)
for row in $(echo "$PERSONAL" | jq -r '.[].scope'); do
    [ "$row" = "personal" ] || fail "scope filter returned non-personal: $row"
done
pass "search scope isolation (personal)"

BRIDGE=$($VIRGIL memory search "Unity" --scope "bridge:enver" --config "$TEST_CONFIG" 2>/dev/null)
BRIDGE_COUNT=$(echo "$BRIDGE" | jq 'length')
[ "$BRIDGE_COUNT" -ge 1 ] || fail "scoped search returned 0"
for row in $(echo "$BRIDGE" | jq -r '.[].scope'); do
    [ "$row" = "bridge:enver" ] || fail "scope filter returned non-bridge: $row"
done
pass "search scope isolation (bridge:enver)"

# ---------- facts ----------

echo "--- facts ---"

FACTS=$($VIRGIL memory facts "Sarah" --config "$TEST_CONFIG" 2>/dev/null)
FACTS_COUNT=$(echo "$FACTS" | jq 'length')
[ "$FACTS_COUNT" -ge 1 ] || fail "facts returned 0 for Sarah"
echo "$FACTS" | jq -e '.[0].type == "fact"' > /dev/null || fail "facts returned non-fact"
pass "facts query by entity name"

TOPIC_FACTS=$($VIRGIL memory facts "meeting preferences" --config "$TEST_CONFIG" 2>/dev/null)
TOPIC_COUNT=$(echo "$TOPIC_FACTS" | jq 'length')
[ "$TOPIC_COUNT" -ge 1 ] || fail "facts returned 0 for topic"
pass "facts query by topic"

# Facts with scope filter
SCOPED_FACTS=$($VIRGIL memory facts "tech stack" --scope "bridge:enver" --config "$TEST_CONFIG" 2>/dev/null)
SCOPED_COUNT=$(echo "$SCOPED_FACTS" | jq 'length')
[ "$SCOPED_COUNT" -ge 1 ] || fail "scoped facts returned 0"
pass "facts with scope filter"

# ---------- fact deduplication ----------

echo "--- fact dedup ---"

# Update existing fact (same topic + entity should update, not create)
BEFORE_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM memory WHERE type='fact' AND topic='meeting preferences'")
$VIRGIL memory store fact "Sarah prefers afternoon meetings now" \
    --topic "meeting preferences" \
    --entities "Sarah:person:subject" \
    --config "$TEST_CONFIG" 2>/dev/null > /dev/null
AFTER_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM memory WHERE type='fact' AND topic='meeting preferences'")
[ "$BEFORE_COUNT" = "$AFTER_COUNT" ] || fail "fact dedup failed: $BEFORE_COUNT -> $AFTER_COUNT"
pass "fact deduplication (update, not insert)"

# Check fact history was written
HISTORY_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM fact_history WHERE fact_id='$FACT_ID'")
[ "$HISTORY_COUNT" -ge 1 ] || fail "fact history not written"
pass "fact history recorded"

# ---------- entity graph ----------

echo "--- entity graph ---"

# Verify entity was stored
ENTITY_COUNT=$(sqlite3 "$TEST_DB" "SELECT count(*) FROM memory_entities WHERE entity='Sarah'")
[ "$ENTITY_COUNT" -ge 1 ] || fail "entity not stored"
pass "entity stored in memory_entities"

# Store another memory referencing Sarah to test graph traversal
$VIRGIL memory store observation "Sarah presented the Q2 roadmap" \
    --topic "roadmap" \
    --entities "Sarah:person:subject" \
    --config "$TEST_CONFIG" 2>/dev/null > /dev/null

# Entity search should find memories connected to Sarah
ENTITY_RESULTS=$($VIRGIL memory search "Sarah" --config "$TEST_CONFIG" 2>/dev/null)
ENTITY_RESULT_COUNT=$(echo "$ENTITY_RESULTS" | jq 'length')
[ "$ENTITY_RESULT_COUNT" -ge 2 ] || fail "entity graph search returned $ENTITY_RESULT_COUNT, expected >= 2"
pass "entity graph search returns connected memories"

echo ""
echo "=== all memory tests passed ==="
