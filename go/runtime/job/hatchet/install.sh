#!/usr/bin/env bash
set -euo pipefail
# Hatchet engine auto-installer
# Fallback chain: docker (only option — Hatchet is server-based)

ENGINE="hatchet"
HATCHET_VERSION="${HATCHET_VERSION:-v0.53.1}"
IMAGE="ghcr.io/hatchet-dev/hatchet:${HATCHET_VERSION}"

if docker inspect "$IMAGE" &>/dev/null; then
    echo "$ENGINE: already installed ($(docker inspect --format '{{.Id}}' "$IMAGE" | head -c 12))"
    exit 0
fi

echo "$ENGINE: pulling $IMAGE..."
docker pull "$IMAGE"
echo "$ENGINE: installed"
