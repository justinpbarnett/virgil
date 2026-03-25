#!/usr/bin/env bash
# test/onboarding.sh -- Stage 7 onboarding tests
# Tests: virgil seed (fact ingestion from markdown), bootstrap skill (external gate).
# Requires ANTHROPIC_API_KEY for seed tests (AI-assisted chunking).
# Set VIRGIL_TEST_EXTERNAL=1 to also run bootstrap (requires all service credentials).
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
    fallback: []
skills:
  dir: skills/
trust:
  auto_approve_threshold: 15
  default_action: ask
EOF

$VIRGIL init --config "$CONFIG" > /dev/null 2>&1
[ -f "$DB" ] || fail "data.db not created"

echo "=== onboarding tests ==="

# ---------- virgil seed ----------
echo "--- virgil seed ---"

# Test 1: seed on empty file succeeds (zero paragraphs -- no error)
EMPTY_FILE="$TEST_DIR/empty.md"
touch "$EMPTY_FILE"
$VIRGIL seed "$EMPTY_FILE" --config "$CONFIG" 2>/dev/null || fail "seed on empty file should return OK (zero paragraphs)"
pass "seed on empty file returns OK"

# Test 2: seed on headings-only file fails (paragraphs exist but all filtered as headings)
HEADINGS_FILE="$TEST_DIR/headings-only.md"
cat > "$HEADINGS_FILE" <<'EOF'
# Section One

## Section Two

### Section Three
EOF
if $VIRGIL seed "$HEADINGS_FILE" --config "$CONFIG" 2>/dev/null; then
  fail "seed on headings-only file should return an error"
fi
pass "seed on headings-only file returns error"

if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo "  SKIP: ANTHROPIC_API_KEY not set -- skipping seed content tests"
else
  # Create a test seed file with known facts
  SEED_FILE="$TEST_DIR/seed-test.md"
  cat > "$SEED_FILE" <<'SEEDEOF'
# Test context

Justin Barnett is an AI engineer and entrepreneur. He runs three main ventures:
Enver (an AI development platform for enterprise teams), Keep (a church management
tool targeting smaller churches), and does consulting for Passion City Church.

Justin lives in Atlanta, Georgia. He has been building software for 10+ years.
His primary programming languages are Go and TypeScript.

## Key relationships

- Alex Rodriguez is Justin's lead engineer at Enver. They work together daily.
- Sarah Chen is the executive director at Passion City Church.
- Keep's target customer is churches with 100-500 members.

## Current priorities

1. Embers VR game (Enver project, launching Q2 2026)
2. Keep outreach pipeline (targeting 50 new church signups per month)
3. Passion City Rock RMS migration (ongoing)
SEEDEOF

  # Test 3: seed runs without error
  SEED_OUT=$($VIRGIL seed "$SEED_FILE" --config "$CONFIG" 2>/dev/null)
  echo "$SEED_OUT" | grep -qi "seeded\|fact" || fail "seed output looks wrong: $SEED_OUT"
  pass "virgil seed runs without error"

  # Test 4: facts are stored in memory
  MEM_OUT=$($VIRGIL memory search "Justin Barnett" --config "$CONFIG" 2>/dev/null)
  echo "$MEM_OUT" | grep -qi "Justin\|Barnett\|engineer\|entrepreneur" || \
    fail "seed facts not found in memory search"
  pass "seeded facts appear in memory search"

  # Test 5: entity facts retrievable
  FACTS_OUT=$($VIRGIL memory facts --about "Justin" --config "$CONFIG" 2>/dev/null)
  [ -n "$FACTS_OUT" ] || fail "no facts found for 'Justin' after seed"
  pass "facts retrievable by entity name"
fi

# ---------- bootstrap skill ----------
echo "--- bootstrap skill ---"

if [ "${VIRGIL_TEST_EXTERNAL:-0}" != "1" ]; then
  echo "  SKIP: VIRGIL_TEST_EXTERNAL=1 not set -- skipping bootstrap test"
else
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    echo "  SKIP: ANTHROPIC_API_KEY not set"
  else
    # Test 6: bootstrap skill runs and creates facts
    BOOT_OUT=$($VIRGIL run bootstrap --config "$CONFIG" 2>/dev/null)
    [ -n "$BOOT_OUT" ] || fail "bootstrap produced no output"
    pass "bootstrap skill runs"

    # Test 7: bootstrap creates person or project facts
    FACTS_OUT=$($VIRGIL memory search "project" --type fact --config "$CONFIG" 2>/dev/null)
    [ -n "$FACTS_OUT" ] && ! echo "$FACTS_OUT" | grep -q '"results":\s*\[\]' || fail "bootstrap created no project facts"
    pass "bootstrap creates facts in memory"
  fi
fi

echo ""
echo "All onboarding tests passed."
