#!/usr/bin/env bats
# apply-services.bats — Tests for templates/shared/apply-services.sh
#
# Run with:
#   bats templates/shared/apply-services.bats

setup() {
  SCRIPT_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
  # shellcheck source=managed-block.sh
  source "$SCRIPT_DIR/managed-block.sh"
  # shellcheck source=emit-docker-compose.sh
  source "$SCRIPT_DIR/emit-docker-compose.sh"
  # shellcheck source=emit-env-example.sh
  source "$SCRIPT_DIR/emit-env-example.sh"
  # shellcheck source=apply-services.sh
  source "$SCRIPT_DIR/apply-services.sh"
  TMP="$(mktemp -d -t apply-svc-bats.XXXXXX)"
  PROJ="$TMP/myapp"
  mkdir -p "$PROJ"

  # Stand up the baseline compose + env files the applier
  # operates on. Both are pre-existing in the scaffold flow.
  emit_docker_compose "$PROJ" "myapp"
  emit_env_example "$PROJ" "myapp"

  COMPOSE="$PROJ/.devcontainer/docker-compose.yml"
  ENVFILE="$PROJ/.env.example"
}

teardown() {
  [[ -n "${TMP:-}" ]] && rm -rf "$TMP"
}

# ----------------------------------------------------------
# postgres
# ----------------------------------------------------------

@test "apply_services postgres: injects postgres service into compose" {
  apply_services "$PROJ" "myapp" "postgres"
  grep -q '^  postgres:$' "$COMPOSE"
  grep -q 'postgres:17-alpine' "$COMPOSE"
  # POSTGRES_DB is interpolated to the project name
  grep -q 'POSTGRES_DB: "myapp"' "$COMPOSE"
}

@test "apply_services postgres: uncomments KIT_QUEUE_POSTGRES_URL" {
  apply_services "$PROJ" "myapp" "postgres"
  grep -q '^KIT_QUEUE_POSTGRES_URL=postgres://dev:dev@localhost:5432/myapp$' "$ENVFILE"
  ! grep -qE '^# KIT_QUEUE_POSTGRES_URL=' "$ENVFILE"
}

@test "apply_services postgres: sets KIT_QUEUE_DRIVER=postgres" {
  apply_services "$PROJ" "myapp" "postgres"
  grep -qE '^KIT_QUEUE_DRIVER=postgres([[:space:]]|$)' "$ENVFILE"
  ! grep -qE '^KIT_QUEUE_DRIVER=sqlite' "$ENVFILE"
}

# ----------------------------------------------------------
# redis
# ----------------------------------------------------------

@test "apply_services redis: injects redis service into compose" {
  apply_services "$PROJ" "myapp" "redis"
  grep -q '^  redis:$' "$COMPOSE"
  grep -q 'redis:8-alpine' "$COMPOSE"
}

@test "apply_services redis: sets KIT_QUEUE_DRIVER=redis" {
  apply_services "$PROJ" "myapp" "redis"
  grep -qE '^KIT_QUEUE_DRIVER=redis([[:space:]]|$)' "$ENVFILE"
  grep -q '^KIT_QUEUE_REDIS_URL=redis://localhost:6379$' "$ENVFILE"
}

# ----------------------------------------------------------
# precedence
# ----------------------------------------------------------

@test "apply_services postgres,redis: redis wins for KIT_QUEUE_DRIVER" {
  apply_services "$PROJ" "myapp" "postgres,redis"
  grep -qE '^KIT_QUEUE_DRIVER=redis([[:space:]]|$)' "$ENVFILE"
  ! grep -qE '^KIT_QUEUE_DRIVER=postgres' "$ENVFILE"
  # Both URLs uncommented
  grep -q '^KIT_QUEUE_REDIS_URL=redis://localhost:6379$' "$ENVFILE"
  grep -q '^KIT_QUEUE_POSTGRES_URL=postgres://dev:dev@localhost:5432/myapp$' "$ENVFILE"
}

@test "apply_services redis,postgres: input order does not change driver winner" {
  apply_services "$PROJ" "myapp" "redis,postgres"
  grep -qE '^KIT_QUEUE_DRIVER=redis([[:space:]]|$)' "$ENVFILE"
}

# ----------------------------------------------------------
# minio
# ----------------------------------------------------------

@test "apply_services minio: injects minio + S3 env vars; leaves queue driver alone" {
  apply_services "$PROJ" "myapp" "minio"
  grep -q '^  minio:$' "$COMPOSE"
  grep -q '^S3_ENDPOINT=http://localhost:9000$' "$ENVFILE"
  grep -q '^S3_ACCESS_KEY=minioadmin$' "$ENVFILE"
  grep -q '^S3_SECRET_KEY=minioadmin$' "$ENVFILE"
  grep -q '^S3_BUCKET=myapp$' "$ENVFILE"
  # Queue driver unchanged
  grep -qE '^KIT_QUEUE_DRIVER=sqlite([[:space:]]|$)' "$ENVFILE"
}

# ----------------------------------------------------------
# all five services
# ----------------------------------------------------------

@test "apply_services all five: injects all catalog services into compose" {
  apply_services "$PROJ" "myapp" "postgres,redis,minio,mailpit,redpanda"
  grep -q '^  postgres:$' "$COMPOSE"
  grep -q '^  redis:$' "$COMPOSE"
  grep -q '^  minio:$' "$COMPOSE"
  grep -q '^  mailpit:$' "$COMPOSE"
  grep -q '^  redpanda:$' "$COMPOSE"
}

@test "apply_services all five: env vars for minio, mailpit, redpanda land in services block" {
  apply_services "$PROJ" "myapp" "postgres,redis,minio,mailpit,redpanda"
  mb_has "$ENVFILE" services
  local body
  body="$(mb_read "$ENVFILE" services)"
  echo "$body" | grep -q '^S3_ENDPOINT='
  echo "$body" | grep -q '^SMTP_HOST='
  echo "$body" | grep -q '^KAFKA_BROKERS='
}

# ----------------------------------------------------------
# Validation
# ----------------------------------------------------------

@test "apply_services foobar: errors on invalid service name" {
  run apply_services "$PROJ" "myapp" "foobar"
  [ "$status" -ne 0 ]
  echo "$output" | grep -qi 'unknown service'
}

@test "apply_services postgres,foobar: errors on any invalid service" {
  run apply_services "$PROJ" "myapp" "postgres,foobar"
  [ "$status" -ne 0 ]
}

@test "apply_services: missing args error" {
  run apply_services "$PROJ" "myapp" ""
  [ "$status" -ne 0 ]
}

# ----------------------------------------------------------
# Idempotency
# ----------------------------------------------------------

@test "apply_services postgres,redis,minio: two runs are byte-identical" {
  apply_services "$PROJ" "myapp" "postgres,redis,minio"
  cp "$COMPOSE" "$TMP/compose.first"
  cp "$ENVFILE" "$TMP/env.first"

  apply_services "$PROJ" "myapp" "postgres,redis,minio"
  cmp "$TMP/compose.first" "$COMPOSE"
  cmp "$TMP/env.first" "$ENVFILE"
}

@test "apply_services all five: two runs are byte-identical" {
  apply_services "$PROJ" "myapp" "postgres,redis,minio,mailpit,redpanda"
  cp "$COMPOSE" "$TMP/compose.first"
  cp "$ENVFILE" "$TMP/env.first"

  apply_services "$PROJ" "myapp" "postgres,redis,minio,mailpit,redpanda"
  cmp "$TMP/compose.first" "$COMPOSE"
  cmp "$TMP/env.first" "$ENVFILE"
}

@test "apply_services: input order doesn't affect output bytes" {
  apply_services "$PROJ" "myapp" "redpanda,mailpit,minio,redis,postgres"
  cp "$COMPOSE" "$TMP/compose.reversed"
  cp "$ENVFILE" "$TMP/env.reversed"

  # Reset and apply with canonical order.
  rm -rf "$PROJ"
  mkdir -p "$PROJ"
  emit_docker_compose "$PROJ" "myapp"
  emit_env_example "$PROJ" "myapp"
  apply_services "$PROJ" "myapp" "postgres,redis,minio,mailpit,redpanda"

  cmp "$TMP/compose.reversed" "$COMPOSE"
  cmp "$TMP/env.reversed" "$ENVFILE"
}

# ----------------------------------------------------------
# Managed-block boundaries
# ----------------------------------------------------------

@test "apply_services: services land inside opted-in services block" {
  apply_services "$PROJ" "myapp" "postgres"
  local body
  body="$(mb_read "$COMPOSE" "opted-in services")"
  echo "$body" | grep -q '^  postgres:$'
}

@test "apply_services: no service definitions appear outside the managed block" {
  apply_services "$PROJ" "myapp" "postgres,redis"
  # Strip the managed block, then verify postgres/redis don't appear
  # in the surrounding bytes (devcontainer service is fine; check the
  # specific service keys we injected).
  local outside
  outside="$(awk '
    /^# >>> kit-managed: opted-in services >>>$/ { skip = 1; next }
    /^# <<< kit-managed: opted-in services <<<$/ { skip = 0; next }
    !skip { print }
  ' "$COMPOSE")"
  ! echo "$outside" | grep -qE '^  (postgres|redis):$'
}

# ----------------------------------------------------------
# apply_no_services
# ----------------------------------------------------------

@test "apply_no_services: removes the telemetry managed block" {
  # Sanity: telemetry block is present before.
  mb_has "$COMPOSE" telemetry

  apply_no_services "$PROJ"

  ! mb_has "$COMPOSE" telemetry
  # devcontainer service preserved
  grep -q '^  devcontainer:$' "$COMPOSE"
  # opted-in services block left in place (user may still want it)
  mb_has "$COMPOSE" "opted-in services"
}

@test "apply_no_services: idempotent (no telemetry block → no-op)" {
  apply_no_services "$PROJ"
  cp "$COMPOSE" "$TMP/compose.first"
  apply_no_services "$PROJ"
  cmp "$TMP/compose.first" "$COMPOSE"
}

@test "apply_no_services: missing compose file is a no-op" {
  rm -f "$COMPOSE"
  run apply_no_services "$PROJ"
  [ "$status" -eq 0 ]
}

# ----------------------------------------------------------
# Named-volume regression guard
# ----------------------------------------------------------

# Stateful services switched from named volumes (pgdata, redisdata,
# miniodata under a top-level `volumes:` block) to bind mounts under
# `./.data/<svc>`. Lock that contract here with a host-independent
# assertion so a future revert doesn't slip through on docker-less
# hosts (where `docker compose config -q` skips).
@test "compose: no top-level volumes: block and no named-volume refs" {
  apply_services "$PROJ" "myapp" "postgres,redis,minio"
  ! grep -q '^volumes:' "$COMPOSE"
  ! grep -qE '^[[:space:]]+(pgdata|redisdata|miniodata):' "$COMPOSE"
}

# ----------------------------------------------------------
# Catalog metadata
# ----------------------------------------------------------

@test "apply_services: substitutes {{PROJECT_NAME}} in postgres healthcheck" {
  apply_services "$PROJ" "wibble" "postgres"
  grep -q 'pg_isready -U dev -d wibble' "$COMPOSE"
}

@test "apply_services: substitutes {{PROJECT_NAME}} in mailpit FROM address" {
  apply_services "$PROJ" "wibble" "mailpit"
  grep -q '^SMTP_FROM=noreply@wibble.local$' "$ENVFILE"
}
