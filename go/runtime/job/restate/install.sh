#!/usr/bin/env bash
set -euo pipefail
# Restate runtime auto-installer
# Fallback: brew > docker pull

ENGINE="restate"
RESTATE_VERSION="${RESTATE_VERSION:-1.3.1}"

if command -v restate-server &>/dev/null; then
    echo "$ENGINE: $(restate-server --version)"
    exit 0
fi

case "$(uname -s)" in
    Darwin)
        if command -v brew &>/dev/null; then
            echo "$ENGINE: installing via brew..."
            brew install restatedev/tap/restate
            echo "$ENGINE: $(restate-server --version)"
            exit 0
        fi
        ;;
esac

# Docker fallback
IMAGE="docker.io/restatedev/restate:${RESTATE_VERSION}"
echo "$ENGINE: pulling $IMAGE..."
docker pull "$IMAGE"
echo "$ENGINE: installed via docker"
