GO ?= go
CONFIG ?= configs/grimoire.sqlite.yaml

.PHONY: fmt vet build test run migrate seed tidy admin

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

# admin builds the React Spectrum SPA and writes the embedded assets into
# internal/admin/dist. Node is required for this target only; the Go build and
# tests never invoke it (a placeholder dist is committed so the embed compiles).
admin:
	cd web/admin && npm ci && npm run build

run:
	$(GO) run ./cmd/grimoire -config $(CONFIG)

migrate:
	$(GO) run ./cmd/grimoire-cli migrate -config $(CONFIG)

seed:
	$(GO) run ./cmd/grimoire-cli seed -config $(CONFIG)
