# VibeSync multi-stage Go base image. See ADR-0001.
#
# This Dockerfile builds a single service binary from source and packages it
# on a distroless static image. It is parameterized by ARGs so each service's
# Dockerfile can `FROM vibesync/base` ... or, more simply, each service can
# copy this Dockerfile and set the args. We use the latter pattern (one
# Dockerfile per service) so docker build contexts stay small.
#
# Usage:
#   docker build -f docker/base.Dockerfile \
#     --build-arg SERVICE_DIR=apps/auth-service \
#     --build-arg BINARY=auth-service .
#
# The result is a non-root, distroless, ~20MB image with no shell — production
# shape from day one.

# --- Build stage ---------------------------------------------------------
FROM golang:1.26-alpine AS builder

# Cache-friendly module layer.
WORKDIR /src

# Copy workspace + module files first for layer caching.
COPY go.work go.work.sum* ./
COPY tools/go.mod tools/go.sum* ./tools/
COPY gen/go/go.mod gen/go/go.sum* ./gen/go/
COPY libs/go.mod libs/go.sum* ./libs/

# Pre-download deps so source-only changes don't re-resolve.
RUN go mod download

# Now the rest of the source.
COPY proto/ ./proto/
COPY gen/go/ ./gen/go/
COPY libs/ ./libs/
COPY apps/ ./apps/

# Which service to build.
ARG SERVICE_DIR
ARG BINARY
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown

RUN test -n "${SERVICE_DIR}" || (echo "SERVICE_DIR build arg required" && false)
RUN test -n "${BINARY}" || (echo "BINARY build arg required" && false)

# Build static, stripped, with version metadata. CGO disabled because we
# target distroless static and none of our deps require it (pgx, kafka-go,
# redis, minio all pure Go).
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath
RUN go build \
	-ldflags "-s -w \
		-X main.buildVersion=${BUILD_VERSION} \
		-X main.buildCommit=${BUILD_COMMIT}" \
	-o /out/"${BINARY}" \
	"./${SERVICE_DIR}"

# --- Runtime stage -------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

ARG BINARY
COPY --from=builder /out/"${BINARY}" /app/"${BINARY}"

# Non-root, no shell, single binary. Service listens on VB_HTTP_ADDR /
# VB_RPC_ADDR env vars (configured at runtime).
USER nonroot:nonroot
WORKDIR /app

# Distroless static has no /etc/passwd healthcheck facility; rely on the
# orchestrator's HTTP /healthz probe instead.
EXPOSE 8080 9090

ENTRYPOINT ["/app/service"]
