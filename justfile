# Release build: static binary, no debug info, reproducible paths.
build:
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o razerctl .

test:
    go test ./...

clean:
    rm -f razerctl
