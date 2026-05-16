#!/usr/bin/env bash
# inspect.sh — run Datasette against a kit instance's data directory.
#
# Read-only, --immutable, safe against a live kit serve writer.
# See docs/inspect-with-datasette.md for the full guide.

set -euo pipefail

PORT="${PORT:-8001}"
HOST="${HOST:-127.0.0.1}"
METADATA="$(dirname "$0")/kit-metadata.json"

if ! command -v datasette >/dev/null 2>&1; then
  echo "datasette not found on PATH. Install with one of:" >&2
  echo "  uv tool install datasette" >&2
  echo "  pipx install datasette" >&2
  exit 1
fi

# Resolve kit's data directory. Fall back to environment override or
# a sensible default. Adjust to match your deployment.
DATA_DIR="${KIT_DATA_DIR:-}"
if [[ -z "$DATA_DIR" ]]; then
  if command -v kit >/dev/null 2>&1; then
    DATA_DIR="$(kit config get serve.data-dir 2>/dev/null || true)"
  fi
fi
if [[ -z "$DATA_DIR" ]]; then
  echo "Unable to resolve kit data directory." >&2
  echo "Set KIT_DATA_DIR or run 'kit config set serve.data-dir <path>'." >&2
  exit 1
fi

# Find every .db file in the data directory.
mapfile -t DB_FILES < <(find "$DATA_DIR" -maxdepth 1 -type f -name '*.db' 2>/dev/null)
if [[ ${#DB_FILES[@]} -eq 0 ]]; then
  echo "No .db files found in $DATA_DIR" >&2
  exit 1
fi

echo "Serving:"
for f in "${DB_FILES[@]}"; do echo "  $f"; done
echo "Metadata: $METADATA"
echo "URL:      http://$HOST:$PORT"
echo

exec datasette serve "${DB_FILES[@]}" \
  --immutable \
  --metadata "$METADATA" \
  --host "$HOST" \
  --port "$PORT" \
  --setting sql_time_limit_ms 10000
