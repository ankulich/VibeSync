# VibeSync Makefile. See ADR-0001, ADR-0002.
#
# This Makefile targets Linux/macOS/CI. On Windows use the equivalent
# scripts/*.sh under Git Bash (scripts/gen-proto.sh, scripts/test.sh, ...).
# The targets below delegate to those scripts where a portable shell form
# is fragile.

GO ?= go

.PHONY: all build test test-short lint proto tidy fmt docker-up docker-down clean help

all: build

# Build every workspace module. We list them explicitly because there is no
# root go.mod (intentional; see ADR-0001).
build:
	$(GO) build ./gen/go/...
	$(GO) build ./libs/...
	@# Apps are built per-service; no apps exist in Phase 1.

# Tests default to short mode (no Docker integration). CI overrides.
test:
	GOTMPDIR=$$(pwd)/.tmp scripts/test.sh

test-short:
	GOTMPDIR=$$(pwd)/.tmp scripts/test.sh --short

# Lint via the toolchain-managed golangci-lint.
lint:
	GOWORK=off $(GO) -C tools tool golangci-lint run ../libs/... ../gen/go/...

# Regenerate protobuf Go code.
proto:
	scripts/gen-proto.sh

# Verify /gen/go is current; used in CI.
proto-check:
	scripts/gen-proto.sh --check

tidy:
	$(GO) -C libs mod tidy
	$(GO) -C gen/go mod tidy
	$(GO) -C tools mod tidy
	$(GO) work sync

fmt:
	gofmt -w libs gen/go
	$(GO) -C tools tool golangci-lint fmt --diff ../libs ../gen/go || true

# Boot the local infrastructure stack (Postgres, Redis, Mongo, Kafka, ...).
docker-up:
	docker compose -f deployments/docker-compose/docker-compose.yml up -d

docker-down:
	docker compose -f deployments/docker-compose/docker-compose.yml down -v

clean:
	rm -rf .tmp .cache tools/.bin
	$(GO) clean -testcache

help:
	@echo "VibeSync Phase 1 targets:"
	@echo "  make build       - build all workspace modules"
	@echo "  make test        - run unit tests (skip integration)"
	@echo "  make test-short  - run only non-integration tests"
	@echo "  make lint        - golangci-lint via go tool"
	@echo "  make proto       - regenerate Go code from .proto"
	@echo "  make proto-check - verify /gen/go is committed and current"
	@echo "  make tidy        - go mod tidy across modules"
	@echo "  make docker-up   - boot local infra (compose)"
	@echo "  make docker-down - tear down local infra"
