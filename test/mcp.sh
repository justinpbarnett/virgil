#!/usr/bin/env bash
# test/mcp.sh -- Stage 6 MCP server acceptance tests
# Starts the MCP server on stdio and sends JSON-RPC messages to verify tool listing.
set -euo pipefail

VIRGIL="./virgil"
TEST_DIR=$(mktemp -d)
TEST_CONFIG="$TEST_DIR/virgil.yaml"

cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }

cat > "$TEST_CONFIG" <<EOF
guide:
  name: Test Guide
  data_dir: $TEST_DIR
ai:
  default: anthropic
  providers:
    anthropic:
      api_key_env: ANTHROPIC_API_KEY
  interactive:
    model: anthropic/sonnet
skills:
  dir: skills/
trust:
  auto_approve_threshold: 15
  default_action: ask
EOF

cd "$(dirname "$0")/.."
CGO_ENABLED=1 go build -tags fts5 -o "$TEST_DIR/virgil" ./cmd/virgil
VIRGIL="$TEST_DIR/virgil"

echo "=== MCP server tests ==="

# Initialize DB
"$VIRGIL" --config "$TEST_CONFIG" init > /dev/null 2>&1

# Use a FIFO for proper interactive JSON-RPC exchange
FIFO=$(mktemp -u)
mkfifo "$FIFO"
MCP_OUTPUT_FILE=$(mktemp)

"$VIRGIL" --config "$TEST_CONFIG" mcp < "$FIFO" > "$MCP_OUTPUT_FILE" 2>/dev/null &
MCP_PID=$!

exec 3>"$FIFO"
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' >&3
sleep 0.3
echo '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >&3
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' >&3
sleep 0.5
exec 3>&-
wait "$MCP_PID" 2>/dev/null || true
rm -f "$FIFO"

MCP_OUTPUT=$(cat "$MCP_OUTPUT_FILE")
rm -f "$MCP_OUTPUT_FILE"

if echo "$MCP_OUTPUT" | grep -q '"tools"'; then
  pass "tools/list returns tool list"
else
  fail "tools/list did not return expected tools response"
fi

if echo "$MCP_OUTPUT" | grep -q '"task_list"'; then
  pass "task_list tool registered in MCP"
else
  fail "task_list not found in MCP tool list"
fi

echo ""
echo "MCP tests passed."
