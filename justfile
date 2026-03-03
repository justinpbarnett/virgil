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

start: build
    #!/usr/bin/env sh
    pid_file="$HOME/.local/share/virgil/virgil.pid"
    if [ -f "$pid_file" ]; then
        pid=$(cat "$pid_file")
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid"
            sleep 0.5
        fi
        rm -f "$pid_file"
    fi
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
