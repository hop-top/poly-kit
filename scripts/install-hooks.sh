#!/usr/bin/env bash
# install-hooks.sh: wire repo-tracked Git hooks (.githooks/) into this clone.
#
# Idempotent: running multiple times re-sets the same config value.
# Per-clone: CI does not need this; it's a local-developer convenience.

set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"

git -C "$HERE" config core.hooksPath .githooks
echo "Configured core.hooksPath=.githooks (repo: $HERE)"
