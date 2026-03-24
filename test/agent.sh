#!/usr/bin/env bash
# test/agent.sh -- Stage 4 acceptance tests
# Verifies: signal -> agent -> tool calls -> response, run_skill, context assembly
set -euo pipefail

VIRGIL=./virgil
TEST_DIR=$(mktemp -d)
CONFIG="$TEST_DIR/virgil.yaml"
DB="$TEST_DIR/data.db"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

# Write test config with haiku for speed/cost
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

echo "=== agent tests ==="

# ---------- signal ----------
echo "--- signal ---"

# Test 1: Basic signal returns a response
RESPONSE=$($VIRGIL signal "What is 2 plus 3? Reply with just the number." --config "$CONFIG" 2>/dev/null)
echo "$RESPONSE" | jq -e '.response' > /dev/null 2>&1 || fail "signal did not return JSON response"
pass "signal returns response"

# Test 2: Agent events logged
AGENT_EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component='agent'")
[ "$AGENT_EVENTS" -gt 0 ] || fail "no agent events logged"
pass "agent events logged ($AGENT_EVENTS)"

# Test 3: Bridge events logged from agent
BRIDGE_EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component LIKE 'bridge:%'")
[ "$BRIDGE_EVENTS" -gt 0 ] || fail "no bridge events from agent"
pass "bridge events logged ($BRIDGE_EVENTS)"

# Test 4: Interaction written to memory
INTERACTION_COUNT=$(sqlite3 "$DB" "SELECT count(*) FROM memory WHERE type='interaction'")
[ "$INTERACTION_COUNT" -gt 0 ] || fail "no interaction written to memory"
pass "interaction stored in memory"

# ---------- tool use ----------
echo "--- tool use ---"

# Test 5: Signal that triggers memory_store
TOOL_RESP=$($VIRGIL signal "Store a fact in memory: my favorite color is blue. Use the memory_store tool with type 'fact', content 'favorite color is blue', and topic 'preferences'. Confirm when done." --config "$CONFIG" 2>/dev/null)
echo "$TOOL_RESP" | jq -e '.response' > /dev/null 2>&1 || fail "tool signal did not return response"
pass "signal with tool instruction returns response"

# Test 6: Tool events logged
TOOL_EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component LIKE 'tool:%'")
[ "$TOOL_EVENTS" -gt 0 ] || fail "no tool events logged"
pass "tool calls logged ($TOOL_EVENTS)"

# Test 7: Memory written by tool
FACT_COUNT=$(sqlite3 "$DB" "SELECT count(*) FROM memory WHERE type='fact' AND (content LIKE '%blue%' OR content LIKE '%color%')")
[ "$FACT_COUNT" -gt 0 ] || fail "fact not written by tool call"
pass "fact written to memory by tool"

# ---------- run_skill ----------
echo "--- run_skill ---"

# Test 8: Run test-echo skill
SKILL_RESP=$($VIRGIL run test-echo --config "$CONFIG" 2>/dev/null)
echo "$SKILL_RESP" | jq -e '.response' > /dev/null 2>&1 || fail "run skill did not return response"
pass "run skill returns response"

# Test 9: Skill events logged
SKILL_EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE component='skill:test-echo'")
[ "$SKILL_EVENTS" -gt 0 ] || fail "no skill events logged"
pass "skill events logged ($SKILL_EVENTS)"

# Test 10: Skill stored memory via tool
ECHO_MEM=$(sqlite3 "$DB" "SELECT count(*) FROM memory WHERE content LIKE '%test-echo%'")
[ "$ECHO_MEM" -gt 0 ] || fail "skill did not write memory"
pass "skill wrote memory via tool"

echo ""
echo "=== all agent tests passed ==="
