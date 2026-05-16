#!/usr/bin/env bash
set -euo pipefail

# Install/enable GitHub Copilot CLI extension

if gh copilot --version &>/dev/null 2>&1; then
  echo "GitHub Copilot CLI already installed."
  gh copilot --version
  exit 0
fi

echo "Installing GitHub Copilot CLI extension..."
gh extension install github/gh-copilot

echo "Installed:"
gh copilot --version
