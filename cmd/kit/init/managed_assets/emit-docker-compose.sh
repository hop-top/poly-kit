#!/usr/bin/env bash
# emit-docker-compose.sh — Emit .devcontainer/docker-compose.yml +
# sibling .devcontainer/otel-config.yaml into a scaffolded project.
#
# Public function:
#   emit_docker_compose <project-dir> <project-name>
#
# Writes:
#   <project-dir>/.devcontainer/docker-compose.yml
#   <project-dir>/.devcontainer/otel-config.yaml
#
# Compose file layout (matches spec §6 of track
# scaffold-emits-mise-toml-devcontainer-compose):
#
#   - The `devcontainer` service is NOT inside a managed block;
#     it is user-extensible.
#   - Two kit-managed blocks live below it:
#       - `telemetry` — default otel-collector + jaeger services
#       - `opted-in services` — empty by default; T-0808 will
#         populate via `--services`.
#
# otel-config.yaml is entirely kit-managed (one unlabeled block).
#
# Idempotent: re-emitting produces byte-identical files because
# managed-block.sh writes via temp + `cmp -s` check, and the
# devcontainer service block is also written deterministically.
#
# Dependencies: source `managed-block.sh` before sourcing this
# file (the dispatcher in scaffold.sh handles ordering).
#
# shellcheck disable=SC2155

# Guard against double-sourcing.
[[ -n "${_KIT_EMIT_DOCKER_COMPOSE_LOADED:-}" ]] && return 0
_KIT_EMIT_DOCKER_COMPOSE_LOADED=1

# ----------------------------------------------------------
# Internal helpers
# ----------------------------------------------------------

# Write the user-extensible `services:` header + `devcontainer`
# service block. Idempotent: only rewritten if the bytes
# outside the managed sections differ. We rebuild the whole
# preamble (everything above the first kit-managed marker)
# deterministically so re-emits stay byte-identical.
_edc_write_preamble() {
  local file="$1" name="$2"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/edc-pre.XXXXXX")" || return 1

  {
    printf 'services:\n'
    printf '  devcontainer:\n'
    printf '    image: mcr.microsoft.com/devcontainers/base:debian\n'
    printf '    volumes:\n'
    printf '      - ..:/workspace:cached\n'
    printf '    command: sleep infinity\n'
    printf '    environment:\n'
    printf '      OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector:4318\n'
    printf '      OTEL_SERVICE_NAME: %s\n' "$name"
    printf '    depends_on:\n'
    printf '      - otel-collector\n'
  } > "$tmp"

  # If the file already exists and the preamble (everything up
  # to but not including the first kit-managed marker) already
  # matches, leave it alone.
  if [[ -f "$file" ]]; then
    local existing_pre
    existing_pre="$(mktemp "${TMPDIR:-/tmp}/edc-exi.XXXXXX")" || {
      rm -f "$tmp"; return 1;
    }
    awk '/^[[:space:]]*#[[:space:]]*>>>[[:space:]]+kit-managed/{exit} {print}' \
      "$file" > "$existing_pre"

    # Trim trailing blank lines from existing_pre for fair
    # comparison (we add a blank separator before the first
    # managed block via mb_write's append logic).
    local cleaned
    cleaned="$(mktemp "${TMPDIR:-/tmp}/edc-cln.XXXXXX")" || {
      rm -f "$tmp" "$existing_pre"; return 1;
    }
    awk 'BEGIN{n=0} {lines[++n]=$0} END{
      last=n
      while (last > 0 && lines[last] == "") last--
      for (i = 1; i <= last; i++) print lines[i]
    }' "$existing_pre" > "$cleaned"

    if cmp -s "$tmp" "$cleaned"; then
      rm -f "$tmp" "$existing_pre" "$cleaned"
      return 0
    fi
    rm -f "$existing_pre" "$cleaned"

    # Preamble drifted (user edited devcontainer service). Don't
    # clobber: leave file alone, only refresh the managed blocks
    # below. This is the safe behaviour for `kit init --update`.
    rm -f "$tmp"
    return 0
  fi

  # Fresh file: write preamble as-is. `mb_write` calls below
  # will append the managed blocks with a blank-line separator.
  mv "$tmp" "$file"
}

# Build the YAML body for the `telemetry` managed block. The
# block is nested under `services:` so each top-level line is
# indented two spaces.
_edc_telemetry_body() {
  cat <<'YAML'
  otel-collector:
    image: otel/opentelemetry-collector-contrib:0.112.0
    command: ["--config=/etc/otel-config.yaml"]
    volumes:
      - ./otel-config.yaml:/etc/otel-config.yaml:ro
    ports:
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP

  jaeger:
    image: jaegertracing/all-in-one:1.62
    ports:
      - "16686:16686" # UI
    environment:
      COLLECTOR_OTLP_ENABLED: "true"
YAML
}

# Body for the `opted-in services` block — empty by default.
# T-0808 will replace this body via mb_write when `--services`
# is passed. We emit a single comment line as a hint so the
# block isn't visually empty, but the test expecting "empty"
# treats this as empty (no real service definitions).
_edc_opted_in_body() {
  cat <<'YAML'
  # postgres, redis, minio, mailpit, redpanda appended here by --services
YAML
}

# otel-config.yaml content (entire file is the managed block).
_edc_otel_config_body() {
  cat <<'YAML'
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  batch: {}

exporters:
  otlp/jaeger:
    endpoint: jaeger:4317
    tls:
      insecure: true
  debug:
    verbosity: normal

service:
  pipelines:
    traces:
      receivers:  [otlp]
      processors: [batch]
      exporters:  [otlp/jaeger, debug]
    metrics:
      receivers:  [otlp]
      processors: [batch]
      exporters:  [debug]
    logs:
      receivers:  [otlp]
      processors: [batch]
      exporters:  [debug]
YAML
}

# ----------------------------------------------------------
# Public API
# ----------------------------------------------------------

emit_docker_compose() {
  local project_dir="$1" project_name="$2"

  if [[ -z "$project_dir" || -z "$project_name" ]]; then
    echo "emit_docker_compose: usage: emit_docker_compose <project-dir> <project-name>" >&2
    return 2
  fi
  if [[ -z "${_KIT_MANAGED_BLOCK_LOADED:-}" ]]; then
    echo "emit_docker_compose: managed-block.sh must be sourced first" >&2
    return 2
  fi

  mkdir -p "$project_dir/.devcontainer"

  local compose="$project_dir/.devcontainer/docker-compose.yml"
  local otel="$project_dir/.devcontainer/otel-config.yaml"

  # 1. devcontainer service (above the markers).
  _edc_write_preamble "$compose" "$project_name"

  # 2. telemetry managed block.
  _edc_telemetry_body | mb_write "$compose" telemetry

  # 3. opted-in services managed block (empty default).
  _edc_opted_in_body | mb_write "$compose" "opted-in services"

  # 4. otel-config.yaml — entire file is one managed block.
  _edc_otel_config_body | mb_write "$otel"
}
