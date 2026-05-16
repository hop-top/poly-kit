#!/usr/bin/env bash
set -euo pipefail

# Install Crush via npm

if command -v crush &>/dev/null; then
  echo "Crush already installed: $(crush --version)"
  exit 0
fi

echo "Installing Crush..."
npm install -g crush-cli

echo "Installed: $(crush --version)"
