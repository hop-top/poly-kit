#!/usr/bin/env bash
set -euo pipefail

# Install Vibe via npm

if command -v vibe &>/dev/null; then
  echo "Vibe already installed: $(vibe --version)"
  exit 0
fi

echo "Installing Vibe..."
npm install -g vibe-cli

echo "Installed: $(vibe --version)"
