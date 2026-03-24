#!/bin/bash
set -euo pipefail

VIRGIL=./virgil
DB_DIR=$(mktemp -d)
CONFIG="$DB_DIR/virgil.yaml"
DB="$DB_DIR/data.db"

cleanup() { rm -rf "$DB_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

# Write test config pointing to temp dir with provider definitions
cat > "$CONFIG" <<EOF
guide:
  name: Test Guide
  data_dir: $DB_DIR
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
        gpt4o-mini: gpt-4o-mini
  interactive:
    model: anthropic/sonnet
    fallback:
      - openai/gpt4o
  mcp:
    model: anthropic/haiku
    fallback:
      - anthropic/sonnet
EOF

# Initialize
$VIRGIL init --config "$CONFIG" > /dev/null 2>&1
[ -f "$DB" ] || fail "data.db not created"

echo "=== bridge tests ==="

# ---------- inference ----------
echo "--- inference ---"

# Test 1: Basic inference returns valid JSON response
RESPONSE=$($VIRGIL ask "What is 2+2? Reply with just the number." --config "$CONFIG" 2>/dev/null)
echo "$RESPONSE" | jq -e '.text' > /dev/null 2>&1 || fail "ask did not return valid JSON with text field"
pass "basic inference returns response"

# Test 2: Response includes token counts
TOKENS_IN=$(echo "$RESPONSE" | jq -r '.tokens_in')
TOKENS_OUT=$(echo "$RESPONSE" | jq -r '.tokens_out')
[ "$TOKENS_IN" -gt 0 ] 2>/dev/null || fail "tokens_in not recorded (got: $TOKENS_IN)"
[ "$TOKENS_OUT" -gt 0 ] 2>/dev/null || fail "tokens_out not recorded (got: $TOKENS_OUT)"
pass "token counts in response"

# Test 3: Response includes model_used
MODEL=$(echo "$RESPONSE" | jq -r '.model_used')
[ -n "$MODEL" ] && [ "$MODEL" != "null" ] || fail "model_used not set"
pass "model_used in response"

# Test 4: Token counts logged to events table
EVENT_TOKENS=$(sqlite3 "$DB" "SELECT tokens_in, tokens_out FROM events WHERE component LIKE 'bridge:%' AND action='inference' ORDER BY id DESC LIMIT 1")
echo "$EVENT_TOKENS" | grep -qE "^[0-9]+\|[0-9]+$" || fail "token counts not in events table"
pass "token counts logged to events"

# Test 5: Inference with explicit model flag
RESPONSE2=$($VIRGIL ask "Say hello" --model anthropic/sonnet --config "$CONFIG" 2>/dev/null)
echo "$RESPONSE2" | jq -e '.text' > /dev/null 2>&1 || fail "explicit model ask failed"
pass "explicit model flag works"

# ---------- embeddings ----------
echo "--- embeddings ---"

# Test 6: Embed returns vector
if [ -n "${OPENAI_API_KEY:-}" ]; then
    EMBEDDING=$($VIRGIL embed "test embedding text" --config "$CONFIG" 2>/dev/null)
    DIM=$(echo "$EMBEDDING" | jq 'length')
    [ "$DIM" -eq 1536 ] || fail "expected 1536 dims, got $DIM"
    pass "embed returns 1536-dim vector"

    # Test 7: Embed event logged
    EMBED_EVENT=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component='bridge:embeddings' AND action='embed'")
    [ "$EMBED_EVENT" -gt 0 ] || fail "embed event not logged"
    pass "embed event logged"
else
    echo "  SKIP: embed tests (OPENAI_API_KEY not set)"
fi

# ---------- fallback ----------
echo "--- fallback ---"

# Test 8: Bad API key triggers error logging
if [ -n "${OPENAI_API_KEY:-}" ]; then
    ORIG_KEY="${ANTHROPIC_API_KEY:-}"
    export ANTHROPIC_API_KEY="sk-invalid-key-for-testing"
    # This should fail on anthropic and fall back to openai
    FALLBACK_RESP=$($VIRGIL ask "Say hi" --model anthropic/sonnet --fallback openai/gpt4o --config "$CONFIG" 2>/dev/null) || true
    export ANTHROPIC_API_KEY="$ORIG_KEY"

    # Check that an error was logged for the anthropic attempt
    ERR_COUNT=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component='bridge:anthropic' AND error IS NOT NULL AND error != ''")
    [ "$ERR_COUNT" -gt 0 ] || fail "no error logged for failed anthropic attempt"
    pass "fallback error logged"
else
    echo "  SKIP: fallback tests (OPENAI_API_KEY not set)"
fi

# ---------- events ----------
echo "--- events ---"

# Test 9: Bridge events recorded in DB
EVENT_COUNT=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component LIKE 'bridge:%'")
[ "$EVENT_COUNT" -gt 0 ] || fail "no bridge events recorded"
pass "bridge events recorded ($EVENT_COUNT)"

echo ""
echo "=== all bridge tests passed ==="
