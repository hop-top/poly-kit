#!/usr/bin/env bash
# shellcheck disable=SC1091
# apply-services.sh — Apply the `--services` catalog into a
# scaffolded project.
#
# Two public entry points:
#
#   apply_services    <project-dir> <project-name> <services-csv>
#   apply_no_services <project-dir>
#
# `apply_services` injects each named service's compose stanza
# into the kit-managed `opted-in services` block of
# `<project-dir>/.devcontainer/docker-compose.yml`, and merges
# matching env vars into `<project-dir>/.env.example`. The
# `queue` managed block of `.env.example` is rewritten in place
# so `KIT_QUEUE_DRIVER` (and the corresponding URL line) reflect
# the selected backend.
#
# Queue driver precedence (highest wins):
#   1. redis     → KIT_QUEUE_DRIVER=redis
#   2. postgres  → KIT_QUEUE_DRIVER=postgres
#   3. otherwise → KIT_QUEUE_DRIVER=sqlite   (default; untouched)
#
# Service-specific env vars (S3_*, SMTP_*, KAFKA_*) are
# appended to a single `services` labeled managed block at the
# bottom of `.env.example`, keeping the per-adapter blocks
# (`queue`, `storage`, `log`, `config`, `telemetry`) focused on
# adapter-level configuration and out of the way of catalog
# additions.
#
# `apply_no_services` strips the `telemetry` managed block from
# the docker-compose.yml. Used by scaffold.sh's `--no-services`
# flag for the rare case of a pure-binary CLI that has no use
# for default telemetry. The user-extensible `devcontainer:`
# service is left intact.
#
# Idempotency: all writes go through `mb_write` / `mb_remove`
# from managed-block.sh — running the same applier twice with
# the same arguments produces byte-identical compose and env
# files.
#
# Dependencies: source `managed-block.sh` before this file
# (scaffold.sh handles ordering; the bats tests source both
# explicitly).

# Guard against double-sourcing
[[ -n "${_KIT_APPLY_SERVICES_LOADED:-}" ]] && return 0
_KIT_APPLY_SERVICES_LOADED=1

# Source managed-block.sh if caller forgot.
if [[ -z "${_KIT_MANAGED_BLOCK_LOADED:-}" ]]; then
  _as_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=managed-block.sh
  source "${_as_dir}/managed-block.sh"
  unset _as_dir
fi

# ----------------------------------------------------------
# Catalog
# ----------------------------------------------------------

# The set of valid service names. Order here is the canonical
# order for applying services so two runs with the same set
# (regardless of input order) produce byte-identical output.
_AS_CATALOG=(postgres redis minio mailpit redpanda)

# Echoes 0 if $1 is a known service name, non-zero otherwise.
_as_is_valid_service() {
  local needle="$1" name
  for name in "${_AS_CATALOG[@]}"; do
    [[ "$name" == "$needle" ]] && return 0
  done
  return 1
}

# Return the directory containing this script's resources
# (the catalog .yml + env/ snippets live alongside it).
_as_resource_dir() {
  cd "$(dirname "${BASH_SOURCE[0]}")" && pwd
}

# Echo the canonical-ordered, deduplicated list of services
# from a comma-separated input. Invalid names cause exit 2 and
# a message on stderr. Avoids bash-4 associative arrays so it
# works on macOS bash 3.2.
_as_parse_services() {
  local input="$1"
  local seen=" "
  local chosen=""
  local svc

  IFS=',' read -ra _as_raw <<< "$input"
  for svc in "${_as_raw[@]}"; do
    svc="$(echo "$svc" | tr -d ' ')"
    [[ -z "$svc" ]] && continue
    if ! _as_is_valid_service "$svc"; then
      echo "apply_services: unknown service: $svc" >&2
      echo "  valid: ${_AS_CATALOG[*]}" >&2
      return 2
    fi
    case "$seen" in
      *" $svc "*) ;;
      *) seen="${seen}${svc} " ;;
    esac
  done

  # Emit in canonical order so input order does not affect
  # output bytes.
  for svc in "${_AS_CATALOG[@]}"; do
    case "$seen" in
      *" $svc "*) chosen="${chosen}${svc}"$'\n' ;;
    esac
  done

  printf '%s' "$chosen"
}

# ----------------------------------------------------------
# Internal: assemble bodies
# ----------------------------------------------------------

# Build the YAML body for the `opted-in services` managed
# block: concatenation of each selected service's compose
# snippet (in canonical order), with `{{PROJECT_NAME}}`
# substituted. Reads service list from $@.
_as_compose_body() {
  local project_name="$1"; shift
  local resource_dir
  resource_dir="$(_as_resource_dir)"
  local svc snippet first=1

  for svc in "$@"; do
    snippet="$resource_dir/services/$svc.yml"
    if [[ ! -f "$snippet" ]]; then
      echo "apply_services: missing snippet: $snippet" >&2
      return 1
    fi
    if [[ $first -eq 0 ]]; then
      printf '\n'
    fi
    first=0
    # Substitute the project-name placeholder. Use awk so we
    # don't depend on sed's BSD-vs-GNU quirks.
    awk -v name="$project_name" '
      { gsub(/\{\{PROJECT_NAME\}\}/, name); print }
    ' "$snippet"
  done
}

# Build the body for the `services` env block in .env.example:
# concatenation of each selected service's `.env` snippet
# (`<resource_dir>/services/env/<svc>.env`) with
# `{{PROJECT_NAME}}` substituted. Skips postgres and redis
# (their vars live in the `queue` block).
_as_env_services_body() {
  local project_name="$1"; shift
  local resource_dir
  resource_dir="$(_as_resource_dir)"
  local svc snippet first=1

  for svc in "$@"; do
    case "$svc" in
      postgres|redis) continue ;;   # handled by _as_env_queue_body
    esac
    snippet="$resource_dir/services/env/$svc.env"
    if [[ ! -f "$snippet" ]]; then
      echo "apply_services: missing env snippet: $snippet" >&2
      return 1
    fi
    if [[ $first -eq 0 ]]; then
      printf '\n'
    fi
    first=0
    awk -v name="$project_name" '
      { gsub(/\{\{PROJECT_NAME\}\}/, name); print }
    ' "$snippet"
  done
}

# Build the body for the `queue` block based on which queue
# services are selected. Driver precedence: redis > postgres
# > sqlite. URL lines are uncommented for selected backends,
# commented for the rest.
_as_env_queue_body() {
  local project_name="$1"; shift
  local has_redis=0 has_postgres=0 svc
  for svc in "$@"; do
    case "$svc" in
      redis)    has_redis=1 ;;
      postgres) has_postgres=1 ;;
    esac
  done

  local driver="sqlite"
  if [[ $has_redis -eq 1 ]]; then
    driver="redis"
  elif [[ $has_postgres -eq 1 ]]; then
    driver="postgres"
  fi

  printf 'KIT_QUEUE_DRIVER=%-7s        # sqlite | redis | postgres\n' "$driver"

  if [[ $has_redis -eq 1 ]]; then
    printf 'KIT_QUEUE_REDIS_URL=redis://localhost:6379\n'
  else
    printf '# KIT_QUEUE_REDIS_URL=redis://localhost:6379\n'
  fi

  if [[ $has_postgres -eq 1 ]]; then
    printf 'KIT_QUEUE_POSTGRES_URL=postgres://dev:dev@localhost:5432/%s\n' "$project_name"
  else
    printf '# KIT_QUEUE_POSTGRES_URL=postgres://...\n'
  fi
}

# ----------------------------------------------------------
# Public: apply_services
# ----------------------------------------------------------

apply_services() {
  local project_dir="$1" project_name="$2" services_csv="$3"

  if [[ -z "$project_dir" || -z "$project_name" || -z "$services_csv" ]]; then
    echo "apply_services: usage: apply_services <project-dir> <project-name> <services-csv>" >&2
    return 2
  fi
  if [[ -z "${_KIT_MANAGED_BLOCK_LOADED:-}" ]]; then
    echo "apply_services: managed-block.sh must be sourced first" >&2
    return 2
  fi

  local compose="$project_dir/.devcontainer/docker-compose.yml"
  local envfile="$project_dir/.env.example"

  if [[ ! -f "$compose" ]]; then
    echo "apply_services: compose file not found: $compose" >&2
    echo "  (run emit_docker_compose first)" >&2
    return 1
  fi
  if [[ ! -f "$envfile" ]]; then
    echo "apply_services: env file not found: $envfile" >&2
    echo "  (run emit_env_example first)" >&2
    return 1
  fi

  # Parse + validate. Reads into a bash array.
  local -a services
  local parsed
  if ! parsed="$(_as_parse_services "$services_csv")"; then
    return 2
  fi
  # shellcheck disable=SC2206
  services=( $parsed )

  if [[ "${#services[@]}" -eq 0 ]]; then
    # Nothing to do (empty / whitespace-only input).
    return 0
  fi

  # 1. Inject compose stanzas into the `opted-in services` block.
  _as_compose_body "$project_name" "${services[@]}" \
    | mb_write "$compose" "opted-in services"

  # 2. Rewrite the queue block (driver + URLs).
  _as_env_queue_body "$project_name" "${services[@]}" \
    | mb_write "$envfile" queue

  # 3. Append (or refresh) the `services` env block with the
  #    non-queue service-specific vars (S3, SMTP, KAFKA).
  local services_body
  services_body="$(_as_env_services_body "$project_name" "${services[@]}")"
  if [[ -n "$services_body" ]]; then
    printf '%s\n' "$services_body" | mb_write "$envfile" services
  elif mb_has "$envfile" services; then
    # Selection has only postgres/redis — drop a stale services
    # block from a previous run, if any.
    mb_remove "$envfile" services
  fi
}

# ----------------------------------------------------------
# Public: apply_no_services
# ----------------------------------------------------------

apply_no_services() {
  local project_dir="$1"

  if [[ -z "$project_dir" ]]; then
    echo "apply_no_services: usage: apply_no_services <project-dir>" >&2
    return 2
  fi
  if [[ -z "${_KIT_MANAGED_BLOCK_LOADED:-}" ]]; then
    echo "apply_no_services: managed-block.sh must be sourced first" >&2
    return 2
  fi

  local compose="$project_dir/.devcontainer/docker-compose.yml"
  if [[ ! -f "$compose" ]]; then
    return 0
  fi

  mb_remove "$compose" telemetry
}
