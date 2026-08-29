#!/usr/bin/env bash
# Bootstrap the VibeSync toolchain: builds buf and protoc plugins from the
# pinned versions in tools/go.mod into tools/.bin. Idempotent and fast on
# repeat runs. Run once after clone, or after pulling changes to tools/go.mod.
#
# This is the only command a contributor needs before `make proto`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TOOLS_DIR="${REPO_ROOT}/tools"
BIN_DIR="${TOOLS_DIR}/.bin"

mkdir -p "${BIN_DIR}"
echo "==> installing toolchain into ${BIN_DIR}"
GOWORK=off GOBIN="${BIN_DIR}" go -C "${TOOLS_DIR}" install \
	github.com/bufbuild/buf/cmd/buf \
	google.golang.org/protobuf/cmd/protoc-gen-go \
	connectrpc.com/connect/cmd/protoc-gen-connect-go \
	github.com/golangci/golangci-lint/v2/cmd/golangci-lint

echo "==> toolchain ready. Add to PATH:"
echo "    export PATH=\"${BIN_DIR}:\$PATH\""
echo "  or invoke via 'go -C ${TOOLS_DIR} tool <name>' (GOWORK=off)."
