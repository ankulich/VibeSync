#!/usr/bin/env bash
# Run unit tests across all workspace modules.
#
# Sets GOTMPDIR to a workspace-local directory. This works around a
# Windows-specific issue where the default temp directory is held by a
# real-time antivirus scan and the test binary cannot be written. The
# workspace-local path is trusted and bypasses the scan.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

export GOTMPDIR="${GOTMPDIR:-${REPO_ROOT}/.tmp}"
mkdir -p "${GOTMPDIR}"

# Pass through any args (e.g. --short, -run X, -v).
echo "==> go test (GOTMPDIR=${GOTMPDIR}) $*"
cd "${REPO_ROOT}"
SHORT_FLAG=""
for arg in "$@"; do
	if [[ "$arg" == "--short" ]]; then
		SHORT_FLAG="-short"
		shift
	fi
done

# gen/go has no tests; build it as a smoke check.
go build ./gen/go/...
go test ${SHORT_FLAG} -count=1 "$@" ./libs/...
echo "OK: tests passed."
