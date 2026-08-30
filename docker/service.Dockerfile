# VibeSync shared Go service image. See ADR-0001.
#
# One parameterized Dockerfile builds any of the Go microservices:
#
#   docker build -f docker/service.Dockerfile \
#     --build-arg SERVICE=auth-service .
#
# docker-compose passes SERVICE per service via build.args. BuildKit cache
# mounts share the Go module + build caches across all service builds, so
# rebuilding one service after a shared-libs change is incremental.
#
# The result is a non-root Alpine image (~25MB) with busybox wget available
# for the compose healthchecks (a distroless runtime has no shell/wget, which
# is why CMD-SHELL healthchecks would not work there).

# --- Build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Workspace + every module's go.mod/go.sum first for layer caching. go.work
# references all app modules, so all module files must exist before
# `go mod download` even though we compile a single service.
COPY go.work go.work.sum* ./
COPY tools/go.mod tools/go.sum* ./tools/
COPY gen/go/go.mod gen/go/go.sum* ./gen/go/
COPY libs/go.mod libs/go.sum* ./libs/
COPY apps/auth-service/go.mod apps/auth-service/go.sum* ./apps/auth-service/
COPY apps/user-service/go.mod apps/user-service/go.sum* ./apps/user-service/
COPY apps/room-service/go.mod apps/room-service/go.sum* ./apps/room-service/
COPY apps/sync-service/go.mod apps/sync-service/go.sum* ./apps/sync-service/
COPY apps/playback-service/go.mod apps/playback-service/go.sum* ./apps/playback-service/
COPY apps/media-service/go.mod apps/media-service/go.sum* ./apps/media-service/
COPY apps/provider-service/go.mod apps/provider-service/go.sum* ./apps/provider-service/
COPY apps/test-clients/go.mod apps/test-clients/go.sum* ./apps/test-clients/

RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Source (context is the repo root; see .dockerignore for exclusions).
COPY proto/ ./proto/
COPY gen/go/ ./gen/go/
COPY libs/ ./libs/
COPY apps/ ./apps/

# Which service to build (e.g. "auth-service" — the apps/ directory name and
# binary name; the cmd lives at apps/${SERVICE}/cmd/${SERVICE}).
ARG SERVICE
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown

RUN test -n "${SERVICE}" || (echo "SERVICE build arg required" && false)

# Static, stripped, with version metadata. CGO disabled: pgx, kafka-go,
# redis and minio are pure Go.
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go build \
		-ldflags "-s -w \
			-X main.buildVersion=${BUILD_VERSION} \
			-X main.buildCommit=${BUILD_COMMIT}" \
		-o /out/service \
		./apps/${SERVICE}/cmd/${SERVICE}

# --- Runtime stage -------------------------------------------------------
# Alpine (not distroless) so the compose CMD-SHELL healthchecks can use
# busybox wget. Pinned minor tag for reproducible-ish local builds.
FROM alpine:3.20

COPY --from=builder /out/service /app/service

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

# Services listen on their per-service web.addr default (8080-8086; see each
# service's internal/config defaults) and expose Prometheus metrics alongside.
EXPOSE 8080 8081 8082 8083 8084 8085 8086 9090

ENTRYPOINT ["/app/service"]
