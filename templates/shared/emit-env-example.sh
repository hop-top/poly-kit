#!/usr/bin/env bash
# shellcheck disable=SC1091
# emit-env-example.sh — Emit `.env.example` with kit-adapter env vars
#
# Writes a project's `.env.example` containing five labeled
# kit-managed blocks (telemetry, storage, queue, log, config)
# per the scaffold-emits-mise-toml-devcontainer-compose spec §7.
#
# Default values match SQLite/local-XDG paths; redis and postgres
# URLs are commented out. `OTEL_SERVICE_NAME` is interpolated
# from the project name. Idempotent via managed-block.sh —
# user-added content above the markers is preserved across
# re-emits.
#
# Public function:
#   emit_env_example <project-dir> <project-name>
#
# Depends on: managed-block.sh (must be sourced separately, or
# this script will source it from its sibling location).
#
# shellcheck disable=SC2034

# Guard against double-sourcing
[[ -n "${_KIT_EMIT_ENV_EXAMPLE_LOADED:-}" ]] && return 0
_KIT_EMIT_ENV_EXAMPLE_LOADED=1

# Source managed-block.sh if not already loaded.
if [[ -z "${_KIT_MANAGED_BLOCK_LOADED:-}" ]]; then
  _eee_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=managed-block.sh
  source "${_eee_dir}/managed-block.sh"
  unset _eee_dir
fi

# ----------------------------------------------------------
# emit_env_example <project-dir> <project-name>
# ----------------------------------------------------------
#
# Writes `<project-dir>/.env.example` containing the five
# labeled kit-managed blocks. Idempotent: re-emitting produces
# a byte-identical file. Preserves user-added content sitting
# outside the markers.
emit_env_example() {
  local project_dir="$1"
  local project_name="$2"

  if [[ -z "$project_dir" ]]; then
    echo "emit_env_example: missing <project-dir>" >&2
    return 2
  fi
  if [[ -z "$project_name" ]]; then
    echo "emit_env_example: missing <project-name>" >&2
    return 2
  fi
  if [[ ! -d "$project_dir" ]]; then
    echo "emit_env_example: directory not found: $project_dir" >&2
    return 1
  fi

  local file="${project_dir}/.env.example"

  # telemetry
  mb_write "$file" telemetry <<EOF
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
OTEL_SERVICE_NAME=${project_name}
EOF

  # storage
  mb_write "$file" storage <<'EOF'
# Resolved from XDG dirs by default; override for testing.
# KIT_CONFIG_DIR=
# KIT_DATA_DIR=
# KIT_STATE_DIR=
EOF

  # queue
  mb_write "$file" queue <<'EOF'
KIT_QUEUE_DRIVER=sqlite        # sqlite | redis | postgres
# KIT_QUEUE_REDIS_URL=redis://localhost:6379
# KIT_QUEUE_POSTGRES_URL=postgres://...
EOF

  # log
  mb_write "$file" log <<'EOF'
KIT_LOG_LEVEL=info             # debug | info | warn | error
KIT_LOG_FORMAT=json            # json | text
EOF

  # config
  mb_write "$file" config <<'EOF'
# KIT_CONFIG_PATH=               # explicit path; otherwise XDG_CONFIG_HOME
EOF
}
