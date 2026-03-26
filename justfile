# Virgil -- personal AI guide

default:
    @just --list

# Build
build:
    CGO_ENABLED=1 go build -tags "fts5" -o virgil ./cmd/virgil/

# Run
init:
    ./virgil init

status:
    ./virgil status

events *ARGS:
    ./virgil events {{ARGS}}

# Memory
memory-store TYPE CONTENT *ARGS:
    ./virgil memory store {{TYPE}} {{CONTENT}} {{ARGS}}

memory-search QUERY *ARGS:
    ./virgil memory search {{QUERY}} {{ARGS}}

memory-facts ABOUT *ARGS:
    ./virgil memory facts {{ABOUT}} {{ARGS}}

# AI bridge
ask MESSAGE *ARGS:
    ./virgil ask {{MESSAGE}} {{ARGS}}

embed TEXT:
    ./virgil embed {{TEXT}}

# Agent
signal MESSAGE *ARGS:
    ./virgil signal {{MESSAGE}} {{ARGS}}

run-skill NAME *ARGS:
    ./virgil run {{NAME}} {{ARGS}}

# Serve
serve:
    ./virgil serve

# Deploy (requires fly CLI and authenticated account)
deploy:
    fly deploy

deploy-status:
    fly status

deploy-logs:
    fly logs

# Test
test-skeleton:
    bash test/skeleton.sh

test-memory:
    bash test/memory.sh

test-bridge:
    bash test/bridge.sh

test-agent:
    bash test/agent.sh

test-smoke:
    bash test/smoke.sh

test-all:
    bash test/skeleton.sh
    bash test/memory.sh
    bash test/bridge.sh
    bash test/agent.sh
    bash test/smoke.sh
