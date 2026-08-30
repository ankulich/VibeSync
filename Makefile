# VibeSync Makefile. See ADR-0001, ADR-0002.
#
# This Makefile targets Linux/macOS/CI. On Windows use the equivalent
# scripts/*.sh under Git Bash (scripts/gen-proto.sh, scripts/test.sh, ...).
# The targets below delegate to those scripts where a portable shell form
# is fragile.

GO ?= go

COMPOSE := docker compose -f deployments/docker-compose/docker-compose.yml

# All Go service modules (apps/frontend is a Node module, built via compose).
APPS := auth-service user-service room-service sync-service playback-service media-service provider-service

.PHONY: all build test test-short lint proto tidy fmt \
	docker-up docker-down docker-reset docker-logs \
	svc-build svc-up svc-restart svc-logs \
	clean help

all: build

# Build every workspace module. We list them explicitly because there is no
# root go.mod (intentional; see ADR-0001).
build:
	$(GO) build ./gen/go/...
	$(GO) build ./libs/...
	@for app in $(APPS); do (cd apps/$$app && $(GO) build ./...) || exit 1; done

# Tests default to short mode (no Docker integration). CI overrides.
test:
	GOTMPDIR=$$(pwd)/.tmp scripts/test.sh

test-short:
	GOTMPDIR=$$(pwd)/.tmp scripts/test.sh --short

# Lint via the toolchain-managed golangci-lint.
lint:
	GOWORK=off $(GO) -C tools tool golangci-lint run ../libs/... ../gen/go/...
	@for app in $(APPS); do (cd apps/$$app && GOWORK=off $(GO) -C ../../tools tool golangci-lint run ./...) || exit 1; done

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
	@for app in $(APPS); do (cd apps/$$app && $(GO) mod tidy) || exit 1; done
	$(GO) work sync

fmt:
	gofmt -w libs gen/go
	@for app in $(APPS); do (cd apps/$$app && gofmt -w .) || exit 1; done
	$(GO) -C tools tool golangci-lint fmt --diff ../libs ../gen/go || true

# --- Docker / compose ------------------------------------------------------

# Boot the whole stack (infra + services + frontend) and wait for health.
docker-up:
	$(COMPOSE) up -d --wait

# Stop the stack but KEEP volumes (data survives).
docker-down:
	$(COMPOSE) down

# Nuclear option: stop and delete all volumes (fresh databases, empty Kafka).
docker-reset:
	$(COMPOSE) down -v
	$(COMPOSE) up -d --wait

docker-logs:
	$(COMPOSE) logs -f --tail=100

# --- Per-service operations ------------------------------------------------
# Usage: make svc-up S=auth-service   (also: user-service, room-service,
#        sync-service, playback-service, media-service, provider-service,
#        frontend)
#
# svc-up rebuilds and redeploys exactly ONE service. --no-deps is the key
# flag: it touches neither the infra stack nor the services that depend on
# the one being updated (no cascading restarts).

svc-build:
	@test -n "$(S)" || (echo "usage: make svc-build S=<service>" && false)
	$(COMPOSE) build $(S)

svc-up:
	@test -n "$(S)" || (echo "usage: make svc-up S=<service>" && false)
	$(COMPOSE) up -d --build --no-deps $(S)

svc-restart:
	@test -n "$(S)" || (echo "usage: make svc-restart S=<service>" && false)
	$(COMPOSE) restart $(S)

svc-logs:
	@test -n "$(S)" || (echo "usage: make svc-logs S=<service>" && false)
	$(COMPOSE) logs -f --tail=200 $(S)

clean:
	rm -rf .tmp .cache tools/.bin
	$(GO) clean -testcache

help:
	@echo "VibeSync targets:"
	@echo "  make build          - build all workspace modules (libs + apps)"
	@echo "  make test           - run unit tests (skip integration)"
	@echo "  make test-short     - run only non-integration tests"
	@echo "  make lint           - golangci-lint via go tool"
	@echo "  make proto          - regenerate Go code from .proto"
	@echo "  make proto-check    - verify /gen/go is committed and current"
	@echo "  make tidy / fmt     - module + format hygiene"
	@echo "  make docker-up      - boot the full stack and wait for health"
	@echo "  make docker-down    - stop the stack (keeps volumes)"
	@echo "  make docker-reset   - stop, wipe volumes, boot fresh"
	@echo "  make docker-logs    - tail all logs"
	@echo "  make svc-up S=svc   - rebuild + redeploy ONE service (--no-deps)"
	@echo "  make svc-build|svc-restart|svc-logs S=svc"
