#!/usr/bin/env bash
set -euo pipefail

# Install Codex CLI via npm

if command -v codex &>/dev/null; then
  echo "Codex CLI already installed: $(codex --version)"
  exit 0
fi

echo "Installing Codex CLI..."
npm install -g @openai/codex

echo "Installed: $(codex --version)"
