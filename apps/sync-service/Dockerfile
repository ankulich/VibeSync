# sync-service Dockerfile. See ADR-0001.
# Uses BuildKit cache mounts so the Go module + build caches are shared
# across all service builds (dramatically speeds up multi-service builds).

# --- Build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Workspace + module files first for layer caching.
COPY go.work go.work.sum* ./
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
COPY tools/go.mod tools/go.sum* ./tools/

RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Source.
COPY proto/ ./proto/
COPY gen/go/ ./gen/go/
COPY libs/ ./libs/
COPY apps/sync-service/ ./apps/sync-service/

ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown

ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	go build \
	-ldflags "-s -w \
		-X main.buildVersion=${BUILD_VERSION} \
		-X main.buildCommit=${BUILD_COMMIT}" \
	-o /out/sync-service \
	./apps/sync-service/cmd/sync-service

# --- Runtime stage -------------------------------------------------------
FROM alpine:latest

COPY --from=builder /out/sync-service /app/sync-service

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app
EXPOSE 8083 9093

ENTRYPOINT ["/app/sync-service"]
