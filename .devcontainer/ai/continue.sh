#!/usr/bin/env bash
set -euo pipefail

# Configure Continue.dev VS Code extension

EXT_ID="Continue.continue"

if code --list-extensions 2>/dev/null | grep -Fqix "$EXT_ID"; then
  echo "Continue.dev extension already installed."
  exit 0
fi

if ! command -v code &>/dev/null; then
  echo "VS Code CLI (code) not found; skipping."
  echo "Install manually: search '$EXT_ID' in Extensions."
  exit 0
fi

echo "Installing Continue.dev extension..."
code --install-extension "$EXT_ID" --force

echo "Continue.dev extension installed."
