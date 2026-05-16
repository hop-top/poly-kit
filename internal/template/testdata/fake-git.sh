#!/bin/bash
# Fake git binary for registry_test.go.
# - Logs all args to FAKE_GIT_LOG (one invocation per line, args space-joined).
# - If FAKE_GIT_FAIL=1, exits 1 without creating the destination.
# - Otherwise, treats the last positional arg as the clone destination,
#   creates it, and writes a stub kit-template.yaml inside.
set -euo pipefail

if [[ -n "${FAKE_GIT_LOG:-}" ]]; then
  printf '%s\n' "$*" >> "$FAKE_GIT_LOG"
fi

if [[ "${FAKE_GIT_FAIL:-0}" == "1" ]]; then
  echo "fake-git: forced failure" >&2
  exit 1
fi

# Last arg is destination for `git clone ... <url> <dest>`.
dest="${@: -1}"
mkdir -p "$dest"
cat > "$dest/kit-template.yaml" <<'EOF'
name: fake-clone
description: Fake clone fixture
kit_version: ">=0.1.0"
variables:
  - name: Name
    required: true
EOF
exit 0
