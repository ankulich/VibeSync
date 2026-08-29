#!/usr/bin/env bash
# Generate Go code from protobuf definitions using buf + local plugins.
# See ADR-0002. No global installs required: tools are pinned in tools/go.mod
# and built into tools/.bin on first run.
#
# Usage: scripts/gen-proto.sh [--check]
#   --check  regenerate to a temp dir and fail if it differs from /gen/go
#            (used in CI to verify generated code is committed and current).
set -euo pipefail

# Resolve repo root regardless of where this is invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TOOLS_DIR="${REPO_ROOT}/tools"
BIN_DIR="${TOOLS_DIR}/.bin"

# Plugins are invoked by buf via PATH, so build them into a local bin dir.
# `go build -o` from the toolchain module pins versions exactly.
build_plugins() {
  mkdir -p "${BIN_DIR}"
  echo "==> building protoc plugins into ${BIN_DIR}"
  # GOWORK=off so the tools module is read directly, not via go.work.
  GOWORK=off GOBIN="${BIN_DIR}" go -C "${TOOLS_DIR}" install \
    github.com/bufbuild/buf/cmd/buf \
    google.golang.org/protobuf/cmd/protoc-gen-go \
    connectrpc.com/connect/cmd/protoc-gen-connect-go
}

# Always rebuild: cheap if up to date, guarantees the pinned versions are used.
build_plugins

export PATH="${BIN_DIR}:${PATH}"

if [[ "${1:-}" == "--check" ]]; then
  echo "==> regenerating to verify /gen/go is current"
  # Generate in place; the caller (CI) diffs against git afterwards.
  (cd "${TOOLS_DIR}" && \
    GOWORK=off "${BIN_DIR}/buf" generate \
      --template "${REPO_ROOT}/buf.gen.yaml" \
      -o "${REPO_ROOT}" "${REPO_ROOT}")
  if ! git -C "${REPO_ROOT}" diff --quiet -- gen/go; then
    echo "ERROR: /gen/go is out of date. Run scripts/gen-proto.sh and commit." >&2
    git -C "${REPO_ROOT}" diff --stat -- gen/go >&2
    exit 1
  fi
  echo "OK: /gen/go is current."
else
  echo "==> generating Go code into ${REPO_ROOT}/gen/go"
  (cd "${TOOLS_DIR}" && \
    GOWORK=off "${BIN_DIR}/buf" generate \
      --template "${REPO_ROOT}/buf.gen.yaml" \
      -o "${REPO_ROOT}" "${REPO_ROOT}")
  echo "OK: generated $(find "${REPO_ROOT}/gen/go" -name '*.go' | wc -l) Go files."
fi
