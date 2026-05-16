#!/usr/bin/env bash
set -euo pipefail
# Temporal server auto-installer
# Fallback: brew > binary download > docker pull

ENGINE="temporal"
TEMPORAL_VERSION="${TEMPORAL_VERSION:-1.25.2}"

if command -v temporal &>/dev/null; then
    echo "$ENGINE: $(temporal --version)"
    exit 0
fi

case "$(uname -s)" in
    Darwin)
        if command -v brew &>/dev/null; then
            echo "$ENGINE: installing via brew..."
            brew install temporal
            echo "$ENGINE: $(temporal --version)"
            exit 0
        fi
        ;;
    Linux)
        ARCH="$(uname -m)"
        case "$ARCH" in
            x86_64) ARCH="amd64" ;;
            aarch64) ARCH="arm64" ;;
        esac
        if [ -w /usr/local/bin ]; then
            BIN_DIR="/usr/local/bin"
        else
            BIN_DIR="$HOME/.local/bin"
            mkdir -p "$BIN_DIR"
        fi
        URL="https://temporal.download/cli/archive/latest?platform=linux&arch=$ARCH"
        echo "$ENGINE: downloading from $URL..."
        curl -sSfL "$URL" | tar xz -C "$BIN_DIR" temporal
        echo "$ENGINE: $(temporal --version)"
        exit 0
        ;;
esac

# Docker fallback
IMAGE="temporalio/auto-setup:${TEMPORAL_VERSION}"
echo "$ENGINE: pulling $IMAGE..."
docker pull "$IMAGE"
echo "$ENGINE: installed via docker"
