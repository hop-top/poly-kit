#!/usr/bin/env bash
set -euo pipefail

# Install Claude Code via npm

if command -v claude &>/dev/null; then
  echo "Claude Code already installed: $(claude --version)"
  exit 0
fi

echo "Installing Claude Code..."
npm install -g @anthropic-ai/claude-code

echo "Installed: $(claude --version)"
