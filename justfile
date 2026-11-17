set shell := ["bash", "-euo", "pipefail", "-c"]

# List the recipes
default:
    @just --list

# Build the binary into bin/dusk
build:
    go build -o bin/dusk ./cmd/dusk

# Run the tests (with the race detector)
test:
    go test -race ./...

# Format the code
fmt:
    gofmt -w .

# Fail if any file is not formatted
fmt-check:
    test -z "$(gofmt -l .)"

# Static analysis
vet:
    go vet ./...

# Official verification: the command approved in .aidlc/*/tasks.md (do not change without a new approval)
check:
    test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...

# Fail if a file has non-ASCII characters (the repository language is English); the `// Author:` header line is exempt
english-check:
    ! grep -rInP '[^\x00-\x7F]' --include='*.go' --include='justfile' --include='*.toml' --include='*.md' --exclude-dir=.aidlc --exclude-dir=.git . | grep -vP '^[^:]+:\d+:// Author: '

# Build into ~/.local/bin/dusk and install the systemd user service (asks before changing anything)
install:
    mkdir -p "$HOME/.local/bin"
    go build -o "$HOME/.local/bin/dusk" ./cmd/dusk
    "$HOME/.local/bin/dusk" install

# Stop and remove the systemd user service (the configuration is kept)
uninstall:
    "$HOME/.local/bin/dusk" uninstall

# Run dusk with arguments, for example: just run check-config
run *args:
    go run ./cmd/dusk {{args}}
