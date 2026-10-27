set shell := ["bash", "-euo", "pipefail", "-c"]

# List recipes
default:
    @just --list --unsorted

# Run all linters and formatters (pre-commit); CI runs this too
lint *args:
    pre-commit run --all-files {{args}}

# Definition of done: lint and test
check: lint test

# Release build: static binary, no debug info, reproducible paths
build:
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o razerctl .

# Run from source, e.g. `just run --help`
run *args:
    go run . {{args}}

# Unit tests
test:
    go test ./...

# Remove build output
clean:
    rm -f razerctl
