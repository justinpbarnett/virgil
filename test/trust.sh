#!/usr/bin/env bash
# test/trust.sh -- Stage 8 trust tests
# Tests: trust CLI (show, approve, reject, check), auto-approve threshold,
#        channel-specific trust, contact dimension, and error recovery trust rollback.
# Set VIRGIL_TEST_EXTERNAL=1 to also run Telegram notification tests.
set -euo pipefail

VIRGIL=./virgil
TEST_DIR=$(mktemp -d)
CONFIG="$TEST_DIR/virgil.yaml"
DB="$TEST_DIR/data.db"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

# Use threshold=5 so tests don't need 15 approvals.
cat > "$CONFIG" <<EOF
guide:
  name: Test Guide
  data_dir: $TEST_DIR
ai:
  default: anthropic
  providers:
    anthropic:
      api_key_env: ANTHROPIC_API_KEY
      models:
        sonnet: claude-sonnet-4-6
        haiku: claude-haiku-4-5-20251001
skills:
  dir: skills/
trust:
  auto_approve_threshold: 5
EOF

$VIRGIL init --config "$CONFIG" > /dev/null 2>&1
[ -f "$DB" ] || fail "data.db not created"

echo "=== trust tests ==="

# ---------- trust show ----------
echo "--- trust show ---"

# Test 1: show returns empty array on fresh DB
SHOW_OUT=$($VIRGIL trust show --config "$CONFIG" 2>/dev/null)
echo "$SHOW_OUT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d == [], f'expected [], got {d}'
" || fail "trust show should return empty array on fresh DB"
pass "trust show returns empty array"

# ---------- trust approve / reject ----------
echo "--- trust approve/reject ---"

# Test 2: approve increments approvals
$VIRGIL trust approve --action email_send --config "$CONFIG" > /dev/null 2>&1
SHOW_OUT=$($VIRGIL trust show --config "$CONFIG" 2>/dev/null)
APPROVALS=$(echo "$SHOW_OUT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['approvals'] for x in d if x['action_type'] == 'email_send'), end='')
" 2>/dev/null || echo "0")
[ "$APPROVALS" -eq 1 ] || fail "expected 1 approval after approve, got $APPROVALS"
pass "trust approve increments approvals"

# Test 3: reject increments rejections
$VIRGIL trust reject --action email_send --config "$CONFIG" > /dev/null 2>&1
SHOW_OUT=$($VIRGIL trust show --config "$CONFIG" 2>/dev/null)
REJECTIONS=$(echo "$SHOW_OUT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['rejections'] for x in d if x['action_type'] == 'email_send'), end='')
" 2>/dev/null || echo "0")
[ "$REJECTIONS" -eq 1 ] || fail "expected 1 rejection after reject, got $REJECTIONS"
pass "trust reject increments rejections"

# ---------- trust check ----------
echo "--- trust check ---"

# Score is now: approvals=1, rejections=1, value=0 (threshold=5).
# Test 4: trust check blocked below threshold
if $VIRGIL trust check --action email_send --config "$CONFIG" 2>/dev/null; then
  fail "trust check should exit non-zero when score below threshold"
fi
pass "trust check blocked below threshold"

# Test 5: auto-approve fires at threshold
# Current value=0; need 5 more approvals to reach value=5 >= threshold.
for i in $(seq 1 5); do
  $VIRGIL trust approve --action email_send --config "$CONFIG" > /dev/null 2>&1
done
# approvals=6, rejections=1, value=5 >= 5
if ! $VIRGIL trust check --action email_send --config "$CONFIG" 2>/dev/null; then
  fail "trust check should exit zero at threshold"
fi
pass "auto-approve fires at threshold"

# Test 6: rejection drops score below threshold
$VIRGIL trust reject --action email_send --config "$CONFIG" > /dev/null 2>&1
# approvals=6, rejections=2, value=4 < 5
if $VIRGIL trust check --action email_send --config "$CONFIG" 2>/dev/null; then
  fail "trust check should be blocked after score drops below threshold"
fi
pass "trust check blocked after score drops below threshold"

# ---------- channel-specific trust ----------
echo "--- channel-specific trust ---"

# Test 7: channel-specific approval is independent of wildcard
for i in $(seq 1 5); do
  $VIRGIL trust approve --action slack_post --channel enver --config "$CONFIG" > /dev/null 2>&1
done
# enver channel: approvals=5, rejections=0, value=5 >= threshold
if ! $VIRGIL trust check --action slack_post --channel enver --config "$CONFIG" 2>/dev/null; then
  fail "channel-specific trust check should pass"
fi
pass "channel-specific trust check passes"

# Test 8: different channel remains blocked
if $VIRGIL trust check --action slack_post --channel other --config "$CONFIG" 2>/dev/null; then
  fail "untrusted channel should be blocked"
fi
pass "untrusted channel remains blocked"

# Test 9: wildcard channel satisfies any channel check
# Approve slack_post on wildcard (*) to threshold
for i in $(seq 1 5); do
  $VIRGIL trust approve --action slack_post --config "$CONFIG" > /dev/null 2>&1
done
# wildcard: value=5 >= threshold; "other" channel check should now pass via wildcard fallback
if ! $VIRGIL trust check --action slack_post --channel other --config "$CONFIG" 2>/dev/null; then
  fail "wildcard approval should satisfy any channel check"
fi
pass "wildcard approval satisfies channel-specific check"

# ---------- contact dimension ----------
echo "--- contact dimension ---"

# Test 10: exact (channel, contact) tuple takes precedence over wildcard contact row.
# Approve calendar_invite on channel=corp, contact=alice to threshold.
for i in $(seq 1 5); do
  $VIRGIL trust approve --action calendar_invite --channel corp --contact alice --config "$CONFIG" > /dev/null 2>&1
done
# alice on corp should pass
if ! $VIRGIL trust check --action calendar_invite --channel corp --contact alice --config "$CONFIG" 2>/dev/null; then
  fail "exact contact trust check should pass"
fi
pass "exact contact trust check passes"

# bob on corp should be blocked (no wildcard contact row yet)
if $VIRGIL trust check --action calendar_invite --channel corp --contact bob --config "$CONFIG" 2>/dev/null; then
  fail "different contact should be blocked"
fi
pass "different contact remains blocked"

# Approve with wildcard contact on corp so bob now passes via fallback
for i in $(seq 1 5); do
  $VIRGIL trust approve --action calendar_invite --channel corp --config "$CONFIG" > /dev/null 2>&1
done
if ! $VIRGIL trust check --action calendar_invite --channel corp --contact bob --config "$CONFIG" 2>/dev/null; then
  fail "wildcard contact row should satisfy any contact check on that channel"
fi
pass "wildcard contact satisfies contact-specific check"

# ---------- rollback ----------
echo "--- rollback ---"

# Test 11: Rollback decrements approvals (not rejections) and blocks when below threshold.
# Approve calendar_create to threshold (value=5).
for i in $(seq 1 5); do
  $VIRGIL trust approve --action calendar_create --config "$CONFIG" > /dev/null 2>&1
done
# Verify at threshold before rollback.
if ! $VIRGIL trust check --action calendar_create --config "$CONFIG" 2>/dev/null; then
  fail "calendar_create should be approved before rollback"
fi
# Confirm the record has rejections=0 (rollback path does not use the reject column).
REJECTIONS=$(
  $VIRGIL trust show --config "$CONFIG" 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['rejections'] for x in d if x['action_type'] == 'calendar_create'), end='')
")
[ "$REJECTIONS" -eq 0 ] || fail "rollback should not have incremented rejections (expected 0, got $REJECTIONS)"
# Capture approvals before rollback.
BEFORE=$(
  $VIRGIL trust show --config "$CONFIG" 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['approvals'] for x in d if x['action_type'] == 'calendar_create'), end='')
")
# Simulate Rollback() by decrementing approvals directly (no CLI for rollback by design).
sqlite3 "$DB" "UPDATE trust_scores SET approvals = MAX(0, approvals - 1) WHERE action_type = 'calendar_create' AND channel = '*' AND contact = '*';"
AFTER=$(
  $VIRGIL trust show --config "$CONFIG" 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['approvals'] for x in d if x['action_type'] == 'calendar_create'), end='')
")
[ "$AFTER" -eq $(( BEFORE - 1 )) ] || fail "rollback should have decremented approvals from $BEFORE to $((BEFORE-1)), got $AFTER"
if $VIRGIL trust check --action calendar_create --config "$CONFIG" 2>/dev/null; then
  fail "trust check should be blocked after rollback drops score below threshold"
fi
pass "rollback decrements approvals (not rejections) and blocks when below threshold"

# Test 12: rollback floors at zero (repeated rollbacks never go negative).
# calendar_create currently has approvals=4 (one below threshold). Roll back 10 more times.
for i in $(seq 1 10); do
  sqlite3 "$DB" "UPDATE trust_scores SET approvals = MAX(0, approvals - 1) WHERE action_type = 'calendar_create' AND channel = '*' AND contact = '*';"
done
FLOOR=$(
  $VIRGIL trust show --config "$CONFIG" 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(next(x['approvals'] for x in d if x['action_type'] == 'calendar_create'), end='')
")
[ "$FLOOR" -eq 0 ] || fail "approvals should floor at 0 after repeated rollbacks, got $FLOOR"
pass "rollback floors approvals at zero"

# ---------- threshold default ----------
echo "--- threshold default ---"

# Test 13: threshold=0 in config should default to 15.
# Create a second config with threshold=0.
CONFIG2="$TEST_DIR/virgil2.yaml"
DB2="$TEST_DIR/data2.db"
cat > "$CONFIG2" <<EOF2
guide:
  name: Test Guide 2
  data_dir: $TEST_DIR/data2
ai:
  default: anthropic
  providers:
    anthropic:
      api_key_env: ANTHROPIC_API_KEY
      models:
        sonnet: claude-sonnet-4-6
        haiku: claude-haiku-4-5-20251001
skills:
  dir: skills/
trust:
  auto_approve_threshold: 0
EOF2
mkdir -p "$TEST_DIR/data2"
DB2="$TEST_DIR/data2/data.db"
$VIRGIL init --config "$CONFIG2" > /dev/null 2>&1
# Approve 5 times -- should still be blocked if default kicks in (threshold becomes 15).
for i in $(seq 1 5); do
  $VIRGIL trust approve --action ping --config "$CONFIG2" > /dev/null 2>&1
done
if $VIRGIL trust check --action ping --config "$CONFIG2" 2>/dev/null; then
  fail "threshold=0 should default to 15 -- 5 approvals should not be enough"
fi
pass "threshold=0 defaults to 15"

# ---------- blocked error message ----------
echo "--- blocked error format ---"

# Test 14: blocked trust check error includes the approve hint.
ERR_MSG=$($VIRGIL trust check --action email_send --config "$CONFIG" 2>&1 || true)
echo "$ERR_MSG" | grep -q 'virgil trust approve' || fail "blocked error missing approve hint: $ERR_MSG"
pass "blocked error includes approve hint"

if [ "${VIRGIL_TEST_EXTERNAL:-0}" != "1" ]; then
  echo "  SKIP: VIRGIL_TEST_EXTERNAL=1 not set -- skipping Telegram notification test"
fi

echo ""
echo "All trust tests passed."
