#!/usr/bin/env bash
# test/smoke.sh -- Stage 9 smoke tests
# Starts virgil serve locally, checks /health, and verifies MCP tools/list.
# For post-Fly-deploy checks, set VIRGIL_URL to the deployed URL instead.
set -euo pipefail

cd "$(dirname "$0")/.."

VIRGIL="./virgil"
TEST_DIR=$(mktemp -d)
CONFIG="$TEST_DIR/virgil.yaml"
SERVE_PID=""

cleanup() {
    [ -n "$SERVE_PID" ] && kill "$SERVE_PID" 2>/dev/null || true
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; exit 1; }
skip() { echo "  SKIP: $1"; }

echo "=== smoke tests ==="

# ---- MCP tools/list (no API key needed) ----

echo "--- MCP ---"

cat > "$CONFIG" <<EOF
guide:
  name: Smoke Test
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
server:
  host: 127.0.0.1
  port: 18080
EOF

"$VIRGIL" --config "$CONFIG" init > /dev/null 2>&1

FIFO=$(mktemp -u)
mkfifo "$FIFO"
MCP_OUT=$(mktemp)

"$VIRGIL" --config "$CONFIG" mcp < "$FIFO" > "$MCP_OUT" 2>/dev/null &
MCP_PID=$!
exec 3>"$FIFO"
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"1.0"}}}' >&3
sleep 0.3
echo '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' >&3
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' >&3
sleep 0.5
exec 3>&-
wait "$MCP_PID" 2>/dev/null || true
rm -f "$FIFO"

OUTPUT=$(cat "$MCP_OUT")
rm -f "$MCP_OUT"

echo "$OUTPUT" | grep -q '"tools"' || fail "MCP tools/list returned no tools"
pass "MCP tools/list"

echo "$OUTPUT" | grep -q '"task_list"' || fail "task_list not in MCP tool list"
pass "task_list registered"

echo "$OUTPUT" | grep -q '"memory_search"' || fail "memory_search not in MCP tool list"
pass "memory_search registered"

# ---- HTTP /health (requires API key for serve to start) ----

echo "--- HTTP health ---"

if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    skip "ANTHROPIC_API_KEY not set -- skipping serve health check"
else
    # Start serve on port 18080
    "$VIRGIL" --config "$CONFIG" serve > "$TEST_DIR/serve.log" 2>&1 &
    SERVE_PID=$!

    # Poll until /health responds (up to 10s)
    for i in $(seq 1 20); do
        if curl -sf http://127.0.0.1:18080/health > /dev/null 2>&1; then
            break
        fi
        if ! kill -0 "$SERVE_PID" 2>/dev/null; then
            echo "serve log:" && cat "$TEST_DIR/serve.log" || true
            fail "serve process exited early"
        fi
        sleep 0.5
    done

    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:18080/health 2>/dev/null)
    [ "$HTTP_CODE" = "200" ] || fail "/health returned HTTP $HTTP_CODE, expected 200"
    HEALTH=$(curl -sf http://127.0.0.1:18080/health 2>/dev/null) || fail "/health did not respond"
    echo "$HEALTH" | grep -q '"ok":true' || fail "/health returned unexpected body: $HEALTH"
    pass "/health returns {\"ok\":true}"

    # Verify server stays alive (no Telegram configured -- nil botDone must not fire)
    sleep 1
    kill -0 "$SERVE_PID" 2>/dev/null || fail "serve exited after startup -- nil botDone channel may be broken"
    pass "serve stays alive with no Telegram config (nil botDone)"

    kill "$SERVE_PID" 2>/dev/null || true
    SERVE_PID=""
fi

# ---- Post-deploy checks (requires VIRGIL_URL) ----

if [ -n "${VIRGIL_URL:-}" ]; then
    echo "--- deployed instance at $VIRGIL_URL ---"
    HEALTH=$(curl -sf "$VIRGIL_URL/health") || fail "$VIRGIL_URL/health did not respond"
    echo "$HEALTH" | grep -q '"ok":true' || fail "/health returned unexpected body"
    pass "$VIRGIL_URL/health"
else
    skip "VIRGIL_URL not set -- skipping deployed instance checks"
fi

echo ""
echo "=== smoke tests passed ==="
