#!/usr/bin/env bats
# emit-docker-compose.bats — Tests for templates/shared/emit-docker-compose.sh
#
# Run with:
#   bats templates/shared/emit-docker-compose.bats

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=managed-block.sh
  source "$SCRIPT_DIR/managed-block.sh"
  # shellcheck source=emit-docker-compose.sh
  source "$SCRIPT_DIR/emit-docker-compose.sh"
  TMP="$(mktemp -d -t edc-bats.XXXXXX)"
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# ----------------------------------------------------------
# Basic emission
# ----------------------------------------------------------

@test "emit_docker_compose: creates .devcontainer directory" {
  emit_docker_compose "$TMP" "myapp"
  [ -d "$TMP/.devcontainer" ]
}

@test "emit_docker_compose: writes docker-compose.yml and otel-config.yaml" {
  emit_docker_compose "$TMP" "myapp"
  [ -f "$TMP/.devcontainer/docker-compose.yml" ]
  [ -f "$TMP/.devcontainer/otel-config.yaml" ]
}

@test "emit_docker_compose: requires both arguments" {
  run emit_docker_compose "$TMP"
  [ "$status" -ne 0 ]
}

# ----------------------------------------------------------
# Compose file contents
# ----------------------------------------------------------

@test "compose: contains devcontainer service" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/docker-compose.yml"
  grep -q '^services:$' "$f"
  grep -q '^  devcontainer:$' "$f"
  grep -q 'mcr.microsoft.com/devcontainers/base:debian' "$f"
}

@test "compose: telemetry block contains otel-collector + jaeger services" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/docker-compose.yml"
  grep -q '^# >>> kit-managed: telemetry >>>$' "$f"
  grep -q '^# <<< kit-managed: telemetry <<<$' "$f"
  grep -q '^  otel-collector:$' "$f"
  grep -q '^  jaeger:$' "$f"
  grep -q 'otel/opentelemetry-collector-contrib:0.112.0' "$f"
  grep -q 'jaegertracing/all-in-one:1.62' "$f"
}

@test "compose: OTEL_SERVICE_NAME matches passed project name" {
  emit_docker_compose "$TMP" "specialname"
  local f="$TMP/.devcontainer/docker-compose.yml"
  grep -q 'OTEL_SERVICE_NAME: specialname' "$f"
}

@test "compose: opted-in services block exists" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/docker-compose.yml"
  grep -q '^# >>> kit-managed: opted-in services >>>$' "$f"
  grep -q '^# <<< kit-managed: opted-in services <<<$' "$f"
}

@test "compose: opted-in services block contains no real service definitions" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/docker-compose.yml"
  # mb_read returns the body between markers
  run mb_read "$f" "opted-in services"
  [ "$status" -eq 0 ]
  # Body must not contain a docker service key like `postgres:`,
  # `redis:`, `minio:`, `mailpit:`, `redpanda:`. Only comments
  # / blank lines are allowed (placeholder hint for T-0808).
  ! echo "$output" | grep -Eq '^  (postgres|redis|minio|mailpit|redpanda):$'
  # And every non-blank line must be a comment.
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    [[ "$line" =~ ^[[:space:]]*# ]] || {
      echo "non-comment line inside opted-in services: $line"
      return 1
    }
  done <<< "$output"
}

@test "compose: depends_on links devcontainer to otel-collector" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/docker-compose.yml"
  grep -q '      - otel-collector' "$f"
}

# ----------------------------------------------------------
# otel-config.yaml contents
# ----------------------------------------------------------

@test "otel-config: entire file is one managed block" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/otel-config.yaml"
  grep -q '^# >>> kit-managed >>>$' "$f"
  grep -q '^# <<< kit-managed <<<$' "$f"
  # First non-blank line should be the open marker (entire file
  # is one block; nothing above it).
  local first
  first="$(awk 'NF{print; exit}' "$f")"
  [ "$first" = "# >>> kit-managed >>>" ]
}

@test "otel-config: contains otlp receivers + jaeger exporter" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/otel-config.yaml"
  grep -q '^receivers:$' "$f"
  grep -q '^  otlp:$' "$f"
  grep -q 'endpoint: 0.0.0.0:4317' "$f"
  grep -q 'endpoint: 0.0.0.0:4318' "$f"
  grep -q '^  otlp/jaeger:$' "$f"
  grep -q 'endpoint: jaeger:4317' "$f"
}

@test "otel-config: pipelines wire traces/metrics/logs" {
  emit_docker_compose "$TMP" "myapp"
  local f="$TMP/.devcontainer/otel-config.yaml"
  grep -q '^    traces:$' "$f"
  grep -q '^    metrics:$' "$f"
  grep -q '^    logs:$' "$f"
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "idempotent: two emits produce byte-identical compose file" {
  emit_docker_compose "$TMP" "myapp"
  cp "$TMP/.devcontainer/docker-compose.yml" "$TMP/compose.first"
  emit_docker_compose "$TMP" "myapp"
  cmp "$TMP/.devcontainer/docker-compose.yml" "$TMP/compose.first"
}

@test "idempotent: two emits produce byte-identical otel-config file" {
  emit_docker_compose "$TMP" "myapp"
  cp "$TMP/.devcontainer/otel-config.yaml" "$TMP/otel.first"
  emit_docker_compose "$TMP" "myapp"
  cmp "$TMP/.devcontainer/otel-config.yaml" "$TMP/otel.first"
}

# ----------------------------------------------------------
# docker compose config validation (opt-in)
# ----------------------------------------------------------

@test "docker compose config: validates the emitted file if docker available" {
  if ! command -v docker >/dev/null 2>&1; then
    skip "docker not installed"
  fi
  if ! docker compose version >/dev/null 2>&1; then
    skip "docker compose plugin unavailable"
  fi
  emit_docker_compose "$TMP" "myapp"
  run docker compose -f "$TMP/.devcontainer/docker-compose.yml" config
  [ "$status" -eq 0 ]
}
