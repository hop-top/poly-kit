#!/usr/bin/env bash
set -euo pipefail

# Install OpenCode via npm

if command -v opencode &>/dev/null; then
  echo "OpenCode already installed: $(opencode --version)"
  exit 0
fi

echo "Installing OpenCode..."
npm install -g opencode-ai

echo "Installed: $(opencode --version)"
