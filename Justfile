build:
    @mkdir -p ./build
    go build -o ./build/bb ./cmd/bb

test:
    go test ./...

# Apply go fix and gofumpt formatting, then vet.
vet:
    go fix ./...
    gofumpt -l -w .
    go vet ./...
