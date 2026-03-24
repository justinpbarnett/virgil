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

# Test
test-skeleton:
    bash test/skeleton.sh

test-memory:
    bash test/memory.sh

test-bridge:
    bash test/bridge.sh

test-all:
    bash test/skeleton.sh
    bash test/memory.sh
    bash test/bridge.sh
