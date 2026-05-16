#!/usr/bin/env bash
# post-create.sh — runs after devcontainer create
set -euo pipefail

echo "==> Installing TS dependencies (pnpm)"
if [ -d sdk/ts ]; then
  (cd sdk/ts && pnpm install)
fi

echo "==> Creating Python venv"
if [ -d sdk/py ]; then
  python3 -m venv sdk/py/.venv
  sdk/py/.venv/bin/pip install --upgrade pip
  if [ -f sdk/py/pyproject.toml ]; then
    if ! sdk/py/.venv/bin/pip install -e "sdk/py[dev]"; then
      echo "==> Editable install with dev extras failed; falling back"
      sdk/py/.venv/bin/pip install -e sdk/py
    fi
  fi
fi

echo "==> Checking for AI tools"
if [ -f .ai-tools ] \
    && [ -f .devcontainer/install-ai-tools.sh ]; then
  bash .devcontainer/install-ai-tools.sh --auto
fi

echo "==> Dev container ready"
