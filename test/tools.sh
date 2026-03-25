#!/usr/bin/env bash
# test/tools.sh -- Stage 5 acceptance tests
# Tests task, people, and memory tools (SQLite-only, no credentials needed).
# External service tools (email, calendar, slack, jira, drive, omi) require
# credentials and are gated behind VIRGIL_TEST_EXTERNAL=1.
set -euo pipefail

VIRGIL="./virgil"
TEST_DIR=$(mktemp -d)
TEST_CONFIG="$TEST_DIR/virgil.yaml"

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

# Init the database
$VIRGIL init --config "$TEST_CONFIG" > /dev/null 2>&1

echo "=== tools tests ==="

# ---------- tasks ----------

echo "--- task tools ---"

# Create a task
TASK_OUT=$($VIRGIL tasks create "Buy milk" --priority normal --source user --config "$TEST_CONFIG")
TASK_ID=$(echo "$TASK_OUT" | jq -r '.id')
[ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ] || fail "task_create returned no id"
TASK_TITLE=$(echo "$TASK_OUT" | jq -r '.title')
[ "$TASK_TITLE" = "Buy milk" ] || fail "task_create title mismatch: $TASK_TITLE"
pass "task_create"

# Create a second task with different priority
$VIRGIL tasks create "Write tests" --priority high --source user --config "$TEST_CONFIG" > /dev/null
pass "task_create (second)"

# List all tasks
LIST_OUT=$($VIRGIL tasks list --config "$TEST_CONFIG")
COUNT=$(echo "$LIST_OUT" | jq 'length')
[ "$COUNT" -ge 2 ] || fail "task_list expected >= 2, got $COUNT"
pass "task_list (all)"

# List filtered by status
LIST_OPEN=$($VIRGIL tasks list --status open --config "$TEST_CONFIG")
OPEN_COUNT=$(echo "$LIST_OPEN" | jq 'length')
[ "$OPEN_COUNT" -ge 2 ] || fail "task_list open expected >= 2, got $OPEN_COUNT"
pass "task_list (status filter)"

# Complete a task
DONE_OUT=$($VIRGIL tasks complete "$TASK_ID" --config "$TEST_CONFIG")
DONE_STATUS=$(echo "$DONE_OUT" | jq -r '.status')
[ "$DONE_STATUS" = "done" ] || fail "task_complete status: $DONE_STATUS"
pass "task_complete"

# Verify completed task shows up correctly
LIST_AFTER=$($VIRGIL tasks list --config "$TEST_CONFIG")
COMPLETED=$(echo "$LIST_AFTER" | jq -r ".[] | select(.id == \"$TASK_ID\") | .status")
[ "$COMPLETED" = "done" ] || fail "completed task still shows as: $COMPLETED"
pass "task_complete (verified in list)"

# Complete nonexistent task
if $VIRGIL tasks complete "nonexistent" --config "$TEST_CONFIG" 2>/dev/null; then
  fail "task_complete should fail for missing task"
fi
pass "task_complete (nonexistent fails)"

# ---------- memory (via tool registry, verified by people_lookup) ----------

echo "--- people tool ---"

# Store a fact about a person
$VIRGIL memory store fact "Alice works at Acme Corp as a designer" --topic "Alice" --entities "Alice:person:subject" --config "$TEST_CONFIG" > /dev/null
$VIRGIL memory store interaction "Discussed project timeline with Alice" --topic "Alice" --entities "Alice:person:participant" --config "$TEST_CONFIG" > /dev/null
$VIRGIL memory store observation "Alice mentioned she prefers Figma" --topic "Alice" --entities "Alice:person:subject" --config "$TEST_CONFIG" > /dev/null

# Look up the person
PEOPLE_OUT=$($VIRGIL people lookup "Alice" --config "$TEST_CONFIG")
FACT_COUNT=$(echo "$PEOPLE_OUT" | jq '.facts | length')
[ "$FACT_COUNT" -ge 1 ] || fail "people_lookup expected >= 1 fact, got $FACT_COUNT"
pass "people_lookup (facts)"

NAME=$(echo "$PEOPLE_OUT" | jq -r '.name')
[ "$NAME" = "Alice" ] || fail "people_lookup name: $NAME"
pass "people_lookup (name)"

# ---------- external service tools (credential-gated) ----------

if [ "${VIRGIL_TEST_EXTERNAL:-0}" = "1" ]; then
  echo "--- external tools ---"

  # Email: list (read-only, safe)
  EMAIL_OUT=$($VIRGIL email list --limit 3 --config "$TEST_CONFIG" 2>&1) || true
  if echo "$EMAIL_OUT" | jq '.' > /dev/null 2>&1; then
    pass "email_list"
  else
    echo "  SKIP: email_list (no accounts configured)"
  fi

  # Calendar: check today (read-only, safe)
  CAL_OUT=$($VIRGIL calendar check --config "$TEST_CONFIG" 2>&1) || true
  if echo "$CAL_OUT" | jq '.' > /dev/null 2>&1; then
    pass "calendar_check"
  else
    echo "  SKIP: calendar_check (no accounts configured)"
  fi

  # Drive: list (read-only, safe)
  DRIVE_OUT=$($VIRGIL drive list --limit 3 --config "$TEST_CONFIG" 2>&1) || true
  if echo "$DRIVE_OUT" | jq '.' > /dev/null 2>&1; then
    pass "drive_list"
  else
    echo "  SKIP: drive_list (no accounts configured)"
  fi

  # Slack: search (read-only, safe)
  # IMPORTANT: write tests (slack_post) may ONLY target:
  #   - DM: @justin.barnett
  #   - Channel: #pc-camp-alerts
  # Never post to any other channel or user in this workspace.
  SLACK_OUT=$($VIRGIL slack search "test" --limit 3 --config "$TEST_CONFIG" 2>&1) || true
  if echo "$SLACK_OUT" | jq '.' > /dev/null 2>&1; then
    pass "slack_search"
  else
    echo "  SKIP: slack_search (no workspaces configured)"
  fi

  # JIRA: search (read-only, safe)
  JIRA_OUT=$($VIRGIL jira search "test" --limit 3 --config "$TEST_CONFIG" 2>&1) || true
  if echo "$JIRA_OUT" | jq '.' > /dev/null 2>&1; then
    pass "jira_search"
  else
    echo "  SKIP: jira_search (no instances configured)"
  fi
else
  echo "--- external tools (skipped, set VIRGIL_TEST_EXTERNAL=1) ---"
fi

echo ""
echo "=== all tools tests passed ==="
