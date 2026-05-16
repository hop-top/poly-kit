#!/usr/bin/env bash
set -euo pipefail

# Install Gemini CLI via npm

if command -v gemini &>/dev/null; then
  echo "Gemini CLI already installed: $(gemini --version)"
  exit 0
fi

echo "Installing Gemini CLI..."
npm install -g @anthropic-ai/gemini-cli \
  || npm install -g @google/gemini-cli

echo "Installed: $(gemini --version)"
