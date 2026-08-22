GO ?= go
CONFIG ?= configs/grimoire.sqlite.yaml

.PHONY: fmt vet build test run migrate seed tidy

fmt:
	gofmt -l -w .

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

test:
	$(GO) test ./...

tidy:
	$(GO) mod tidy

run:
	$(GO) run ./cmd/grimoire -config $(CONFIG)

migrate:
	$(GO) run ./cmd/grimoire-cli migrate -config $(CONFIG)

seed:
	$(GO) run ./cmd/grimoire-cli seed -config $(CONFIG)
