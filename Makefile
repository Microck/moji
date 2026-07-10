.PHONY: build test verify

build:
	go build -o moji ./cmd/moji

test:
	go test ./cmd/... ./internal/... ./e2e/...

verify:
	go vet ./cmd/... ./internal/... ./e2e/...
	go test ./cmd/... ./internal/... ./e2e/...
	go build -o /dev/null ./cmd/moji
