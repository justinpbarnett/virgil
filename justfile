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

# Test
test-skeleton:
    bash test/skeleton.sh

test-all:
    bash test/skeleton.sh
