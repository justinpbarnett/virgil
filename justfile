default: build

build:
    go build -o bin/virgil ./cmd/virgil

test:
    go test ./... -v -count=1

start: build
    ./bin/virgil

server: build
    ./bin/virgil --server

auth:
    go build -o bin/auth ./cmd/auth
    ./bin/auth

lint:
    golangci-lint run ./...
