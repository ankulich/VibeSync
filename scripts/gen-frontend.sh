#!/usr/bin/env bash
# Generate TypeScript code from protobuf definitions for the frontend.
# Requires apps/frontend/node_modules to be installed (npm install).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TOOLS_DIR="${REPO_ROOT}/tools"
FRONTEND_BIN="${REPO_ROOT}/apps/frontend/node_modules/.bin"

if [ ! -f "${FRONTEND_BIN}/protoc-gen-es" ]; then
  echo "ERROR: frontend deps not installed. Run: cd apps/frontend && npm install" >&2
  exit 1
fi

# Put the npm plugins on PATH so buf can find them as local plugins.
export PATH="${FRONTEND_BIN}:${PATH}"

echo "==> generating TypeScript code into apps/frontend/src/gen"
(cd "${TOOLS_DIR}" && \
  GOWORK=off go tool buf generate \
    --template "${REPO_ROOT}/buf.gen.ts.yaml" \
    -o "${REPO_ROOT}" "${REPO_ROOT}")

COUNT=$(find "${REPO_ROOT}/apps/frontend/src/gen" -name '*.ts' | wc -l)
echo "OK: generated ${COUNT} TypeScript files."
