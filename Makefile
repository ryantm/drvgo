.PHONY: build test integration
build:
	go build -o bin/drvgo ./cmd/drvgo
test:
	go test ./...
	go vet ./...
integration:
	go test -tags integration -v ./internal/export

.PHONY: dynamic
dynamic:
	./tests/dynamic.sh
