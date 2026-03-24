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

# Test
test-skeleton:
    bash test/skeleton.sh

test-memory:
    bash test/memory.sh

test-all:
    bash test/skeleton.sh
    bash test/memory.sh
