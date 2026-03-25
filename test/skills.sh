#!/usr/bin/env bash
# test/skills.sh -- Stage 7 acceptance tests
# Verifies: skill loading, scheduler registration, virgil run, skill output, memory integration.
# Requires ANTHROPIC_API_KEY for run tests (actual model calls).
set -euo pipefail

VIRGIL=./virgil
TEST_DIR=$(mktemp -d)
CONFIG="$TEST_DIR/virgil.yaml"
DB="$TEST_DIR/data.db"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

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
    openai:
      api_key_env: OPENAI_API_KEY
      embedding_model: text-embedding-3-small
      models:
        gpt4o: gpt-4o
  interactive:
    model: anthropic/haiku
    fallback:
      - openai/gpt4o
  mcp:
    model: anthropic/haiku
    fallback:
      - anthropic/sonnet
skills:
  dir: test/fixtures/skills
trust:
  auto_approve_threshold: 15
  default_action: ask
EOF

$VIRGIL init --config "$CONFIG" > /dev/null 2>&1
[ -f "$DB" ] || fail "data.db not created"

echo "=== skills tests ==="

# ---------- skill loading ----------
echo "--- skill loading ---"

# Test 1: status shows skills loaded from fixture dir
STATUS_OUT=$($VIRGIL status --config "$CONFIG" 2>/dev/null)
echo "$STATUS_OUT" | grep -q '"skills"' || fail "status output missing skills field"
SKILL_COUNT=$(echo "$STATUS_OUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['skills'])" 2>/dev/null || echo "0")
[ "$SKILL_COUNT" -ge 1 ] || fail "status reports 0 skills in fixture dir (expected at least 1)"
pass "status shows skills count ($SKILL_COUNT)"

# ---------- run skill ----------
echo "--- virgil run ---"

# Test 2: unknown skill exits non-zero
if $VIRGIL run no-such-skill --config "$CONFIG" 2>/dev/null; then
  fail "virgil run with unknown skill should exit non-zero"
fi
pass "virgil run unknown skill exits non-zero"

if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "  SKIP: ANTHROPIC_API_KEY not set -- skipping model run tests"
else
  # Test 3: run test-echo skill
  RUN_OUT=$($VIRGIL run test-echo --config "$CONFIG" 2>/dev/null)
  echo "$RUN_OUT" | grep -qi "echo" || fail "test-echo did not produce expected output"
  pass "run test-echo produces output"

  # Test 4: test-echo stores observation in memory (check any entry was stored, not exact content)
  MEM_OUT=$($VIRGIL memory search "test-echo" --config "$CONFIG" 2>/dev/null)
  [ -n "$MEM_OUT" ] || fail "no memory observations found after test-echo run"
  pass "run test-echo stores observation in memory"
fi

# ---------- production skill loading ----------
echo "--- production skills load ---"

# Test 5: all 7 production skills load without error (no SKILL.md parse errors)
cat > "$TEST_DIR/prod-config.yaml" <<EOF
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
EOF

PROD_STATUS=$($VIRGIL status --config "$TEST_DIR/prod-config.yaml" 2>&1)
echo "$PROD_STATUS" | grep -qi "error\|panic" && fail "production skills failed to load: $PROD_STATUS"
pass "production skills load without error"

# Test 6: production skill count is exactly 7
PROD_SKILL_COUNT=$(echo "$PROD_STATUS" | python3 -c "import sys,json; print(json.load(sys.stdin)['skills'])" 2>/dev/null || echo "0")
[ "$PROD_SKILL_COUNT" -eq 7 ] || fail "expected 7 production skills, got $PROD_SKILL_COUNT"
pass "all 7 production skills counted in status"

echo ""
echo "All skills tests passed."
