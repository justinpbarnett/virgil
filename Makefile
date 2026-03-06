VERSION?=0.1.0
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-X github.com/justinpbarnett/virgil/internal/version.version=$(VERSION) \
                  -X github.com/justinpbarnett/virgil/internal/version.gitCommit=$(GIT_COMMIT) \
                  -X github.com/justinpbarnett/virgil/internal/version.buildTime=$(BUILD_TIME)"

.PHONY: build test version clean

build:
	go build $(LDFLAGS) -o bin/virgil ./

test:
	go test -v ./...

version:
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(GIT_COMMIT)"  
	@echo "Built:   $(BUILD_TIME)"

clean:
	rm -rf bin/

release: test
	@echo "Building release version $(VERSION)"
	go build $(LDFLAGS) -o bin/virgil ./