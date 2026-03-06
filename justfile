default: build

build: build-pipes
    go build -ldflags "-X github.com/justinpbarnett/virgil/internal/version.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev) -X github.com/justinpbarnett/virgil/internal/version.gitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X github.com/justinpbarnett/virgil/internal/version.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/virgil ./cmd/virgil

build-if-changed:
    #!/usr/bin/env sh
    if [ ! -f bin/virgil ] || [ -n "$(find . -name '*.go' -newer bin/virgil -not -path './vendor/*' -print -quit)" ] || [ go.mod -nt bin/virgil ] || [ go.sum -nt bin/virgil ]; then
        just build
    fi

build-pipes:
    #!/usr/bin/env sh
    for cmd in internal/pipes/*/cmd; do
        pipe_dir="$(dirname "$cmd")"
        echo "building $pipe_dir/run"
        go build -o "$pipe_dir/run" "./$cmd"
    done

check: lint test

test:
    go test ./... -v -count=1

start: build-if-changed
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
    if [ -f "$HOME/.config/virgil/voice.json" ]; then
        mkdir -p "$HOME/.local/share/virgil/logs"
        ./bin/virgil --voice >>"$HOME/.local/share/virgil/logs/voice-$(date +%Y-%m-%d).log" 2>&1 &
        voice_pid=$!
        trap "kill $voice_pid 2>/dev/null" EXIT
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
    $HOME/go/bin/golangci-lint run ./...

clean:
    rm -rf bin/
    rm -f internal/pipes/*/run

version:
    @echo "Version: $(git describe --tags --always --dirty 2>/dev/null || echo dev)"
    @echo "Commit:  $(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    @echo "Built:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
