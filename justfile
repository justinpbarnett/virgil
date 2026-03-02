default: build

build: build-pipes
    go build -o bin/virgil ./cmd/virgil

build-pipes:
    #!/usr/bin/env sh
    for cmd in internal/pipes/*/cmd; do
        pipe_dir="$(dirname "$cmd")"
        echo "building $pipe_dir/run"
        go build -o "$pipe_dir/run" "./$cmd"
    done

test:
    go test ./... -v -count=1

start: build stop
    ./bin/virgil

server: build stop
    ./bin/virgil --server

stop:
    -pkill -f "bin/virgil" 2>/dev/null
    @sleep 0.5

auth:
    go build -o bin/auth ./cmd/auth
    ./bin/auth

lint:
    golangci-lint run ./...
