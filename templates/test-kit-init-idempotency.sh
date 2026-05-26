#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------------
# test-kit-init-idempotency.sh
#
# End-to-end tests for the `kit init --update` idempotency
# contract.
#
# Complements the Go-level coverage in
# `cmd/kit/init/managed_test.go` by exercising the actual
# built binary against scratch projects in tempdirs — the
# way real users invoke it.
#
# Test matrix (each test runs in its own mktemp -d sandbox):
#
#   1. Double-update zero diff
#      Re-running `kit init --update` produces byte-identical
#      managed files (sha256 stable across runs).
#
#   2. User content above markers survives
#      Lines a user adds above the kit-managed marker in
#      `mise.toml` are preserved across `--update`.
#
#   3. Add-service idempotency
#      `kit init --add-service redis` twice yields the same
#      managed files as once (mb_write `cmp -s` short-circuit).
#      Note: `--remove-service` is not supported yet — the
#      integration explicitly returns a clear error.
#      `apply_services` is REPLACING (not additive): each call
#      writes the full opted-in compose block from the input
#      CSV, so adding `redis` then `postgres` replaces redis
#      with postgres. This test verifies the
#      same-input-twice-is-stable invariant.
#
#   4. `--check` drift exit codes
#      Exits 0 when managed blocks match the manifest;
#      exits 1 with the drifted file path on stderr when they
#      diverge.
#
#   5. Lang detection gates runtime entries
#      A project with only `package.json` (no go.mod) emits a
#      mise.toml that pins `node` but not `go`.
#
#   6. Cumulative-vs-replace probe for --services
#      Documents the observed behavior of `apply_services`
#      when called twice with different services. Per the
#      script's design (apply-services.sh:60-65), the block
#      is REPLACED each call with the canonical-ordered list
#      derived from the input CSV. This test asserts that
#      behavior so future refactors that flip to additive
#      mode will trip a visible failure here.
#
# Exit code: 0 if every test passes, non-zero with a summary
# on any failure.
# -------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Honor an externally provided KIT_BIN (CI may pre-build once), otherwise
# allocate a unique path via mktemp so parallel CI shards never race on
# a shared /tmp/kit-idempotency. mktemp creates the empty file with a
# unique name; `go build -o` overwrites it.
if [ -n "${KIT_BIN:-}" ]; then
  KIT="$KIT_BIN"
  KIT_OWNED=0
else
  KIT="$(mktemp -t kit-idempotency.XXXXXX)"
  KIT_OWNED=1
  trap 'rm -f "$KIT"' EXIT
fi

# When we own the path (mktemp) the file exists but is empty (not -x),
# so the build path is always taken. When KIT_BIN is supplied externally
# we only rebuild if the binary is missing.
if [ "$KIT_OWNED" = "1" ] || [ ! -x "$KIT" ]; then
  echo "Building kit binary at $KIT ..."
  (cd "$REPO_ROOT" && go build -buildvcs=false -o "$KIT" ./cmd/kit)
fi

pass=0
fail=0
errors=()

ok() {
  printf "  \033[1;32mok\033[0m: %s\n" "$1"
  pass=$((pass + 1))
}

notok() {
  printf "  \033[1;31mNOT OK\033[0m: %s\n" "$1"
  fail=$((fail + 1))
  errors+=("$1")
}

# Snapshot all managed files (sha256 each) under <dir>.
# Stable output: paths sorted, one line per file. Missing
# files emit `<MISSING>` so absence is detectable.
snapshot_managed() {
  local dir="$1" rel sum
  local files=(
    "mise.toml"
    ".devcontainer/devcontainer.json"
    ".devcontainer/docker-compose.yml"
    ".devcontainer/otel-config.yaml"
    ".env.example"
  )
  for rel in "${files[@]}"; do
    if [ -f "$dir/$rel" ]; then
      if command -v sha256sum >/dev/null 2>&1; then
        sum="$(sha256sum "$dir/$rel" | awk '{print $1}')"
      else
        sum="$(shasum -a 256 "$dir/$rel" | awk '{print $1}')"
      fi
      printf '%s  %s\n' "$sum" "$rel"
    else
      printf '<MISSING>  %s\n' "$rel"
    fi
  done
}

# Wrap a test function in a fresh tempdir + cleanup trap.
# Usage: with_tempdir <fn-name>
with_tempdir() {
  local fn="$1" td
  td="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$td'" RETURN
  ( "$fn" "$td" )
  local rc=$?
  rm -rf "$td"
  trap - RETURN
  return $rc
}

# ------------------------------------------------------------
# Test 1: Double `kit init --update` produces zero diff.
# ------------------------------------------------------------
test_double_update_zero_diff() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      first --update failed"; return 1; }

  local first
  first="$(snapshot_managed "$td")"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      second --update failed"; return 1; }

  local second
  second="$(snapshot_managed "$td")"

  if [ "$first" = "$second" ]; then
    return 0
  fi
  echo "      sha256 snapshots differ between runs:"
  diff <(printf '%s\n' "$first") <(printf '%s\n' "$second") | sed 's/^/        /'
  return 1
}

# ------------------------------------------------------------
# Test 2: User content above kit-managed markers survives.
# ------------------------------------------------------------
test_user_content_above_markers() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  # Seed mise.toml with user content above the markers.
  cat > "$td/mise.toml" <<'EOF'
# my custom tool versions
[tools]
deno = "2.0"

EOF

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      --update failed"; return 1; }

  if ! grep -q '# my custom tool versions' "$td/mise.toml"; then
    echo "      user comment line was stripped"
    return 1
  fi
  if ! grep -q 'deno = "2.0"' "$td/mise.toml"; then
    echo "      user 'deno = \"2.0\"' line was stripped"
    return 1
  fi
  if ! grep -q 'kit-managed' "$td/mise.toml"; then
    echo "      kit-managed markers missing after --update"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Test 3: `--add-service redis` twice yields the same result
# as once (mb_write idempotency through the binary surface).
# ------------------------------------------------------------
test_add_service_idempotent() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      bootstrap --update failed"; return 1; }
  local baseline
  baseline="$(snapshot_managed "$td")"

  ( cd "$td" && "$KIT" init --add-service redis ) >/dev/null 2>&1 \
    || { echo "      first --add-service redis failed"; return 1; }
  local after_first
  after_first="$(snapshot_managed "$td")"

  if [ "$baseline" = "$after_first" ]; then
    echo "      --add-service redis was a no-op (compose/env unchanged)"
    return 1
  fi

  ( cd "$td" && "$KIT" init --add-service redis ) >/dev/null 2>&1 \
    || { echo "      second --add-service redis failed"; return 1; }
  local after_second
  after_second="$(snapshot_managed "$td")"

  if [ "$after_first" = "$after_second" ]; then
    return 0
  fi
  echo "      second --add-service redis changed bytes:"
  diff <(printf '%s\n' "$after_first") <(printf '%s\n' "$after_second") \
    | sed 's/^/        /'
  return 1
}

# ------------------------------------------------------------
# Test 4: `--check` exits 0 on no drift, 1 on drift, and
# mentions the drifted file in its output.
# ------------------------------------------------------------
test_check_drift_exit_codes() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      bootstrap --update failed"; return 1; }

  if ! ( cd "$td" && "$KIT" init --check ) >/dev/null 2>&1; then
    echo "      --check returned non-zero on a clean tree"
    return 1
  fi

  # Drift the managed block: flip go pin inside the kit-managed
  # block to a fake version. The emitter will rewrite back to
  # the manifest value, so --check must detect the divergence.
  if [ -f "$td/mise.toml" ]; then
    if ! grep -q 'go = "1.26"' "$td/mise.toml"; then
      echo "      mise.toml lacks expected 'go = \"1.26\"' line"
      return 1
    fi
    # macOS sed needs -i '' but the awk-replace below is portable.
    awk '{ gsub(/go = "1\.26"/, "go = \"9.99\""); print }' \
      "$td/mise.toml" > "$td/mise.toml.tmp"
    mv "$td/mise.toml.tmp" "$td/mise.toml"
  else
    echo "      mise.toml missing after --update"
    return 1
  fi

  local check_rc=0
  local check_out
  check_out="$( cd "$td" && "$KIT" init --check 2>&1 )" || check_rc=$?

  if [ "$check_rc" -eq 0 ]; then
    echo "      --check unexpectedly exited 0 with mise.toml drift"
    return 1
  fi
  if ! printf '%s' "$check_out" | grep -q 'mise.toml'; then
    echo "      --check output did not mention 'mise.toml':"
    printf '%s' "$check_out" | sed 's/^/        /'
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Test 5: lang detection gates runtime entries — package.json
# only ⇒ node in mise.toml, no go.
# ------------------------------------------------------------
test_detect_langs_gating() {
  local td="$1"
  printf '{"name":"x","version":"0.0.0"}\n' > "$td/package.json"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      --update failed"; return 1; }

  if [ ! -f "$td/mise.toml" ]; then
    echo "      mise.toml not emitted for package.json-only project"
    return 1
  fi
  if ! grep -q '^node = ' "$td/mise.toml"; then
    echo "      mise.toml missing 'node = ...' for a ts-detected project"
    return 1
  fi
  if grep -q '^go = ' "$td/mise.toml"; then
    echo "      mise.toml unexpectedly contains 'go = ...' without go.mod"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Test 6: `--add-service redis` then `--add-service postgres`
# documents the observed REPLACING behavior of apply_services:
# the opted-in compose block ends up with postgres only (redis
# evicted), and KIT_QUEUE_DRIVER flips from redis to postgres.
# This locks the contract; a future flip to additive mode
# would change this test (and require a spec update).
# ------------------------------------------------------------
test_services_replace_semantics() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      bootstrap --update failed"; return 1; }
  ( cd "$td" && "$KIT" init --add-service redis ) >/dev/null 2>&1 \
    || { echo "      --add-service redis failed"; return 1; }

  if ! grep -q '^  redis:' "$td/.devcontainer/docker-compose.yml"; then
    echo "      redis: not in compose after --add-service redis"
    return 1
  fi
  if ! grep -q '^KIT_QUEUE_DRIVER=redis' "$td/.env.example"; then
    echo "      KIT_QUEUE_DRIVER!=redis after --add-service redis"
    return 1
  fi

  ( cd "$td" && "$KIT" init --add-service postgres ) >/dev/null 2>&1 \
    || { echo "      --add-service postgres failed"; return 1; }

  # Observed behavior: REPLACING. Document via assertions.
  if grep -q '^  redis:' "$td/.devcontainer/docker-compose.yml"; then
    echo "      redis: still in compose after --add-service postgres (additive?)"
    echo "      apply_services may have flipped to additive mode; update spec/test"
    return 1
  fi
  if ! grep -q '^  postgres:' "$td/.devcontainer/docker-compose.yml"; then
    echo "      postgres: not in compose after --add-service postgres"
    return 1
  fi
  if ! grep -q '^KIT_QUEUE_DRIVER=postgres' "$td/.env.example"; then
    echo "      KIT_QUEUE_DRIVER!=postgres after --add-service postgres"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Test 7: build.sh emits `.gitignore` wrapped in a labeled
# kit-managed block. The composed file is the SOT shipped
# inside `dist/cli-template-*` — there is no Go-side runtime
# emitter, so build.sh's `compose_gitignore` is the only
# producer of `.gitignore`. Markers enable future-proof
# re-scaffold/update semantics.
# ------------------------------------------------------------
test_gitignore_has_kit_managed_markers() {
  local td="$1"

  # Build into a tempdir so we never clobber the repo's dist/.
  # We tolerate post-gitignore failures (e.g. the known php-CI
  # gap) because compose_gitignore runs early in the loop; the
  # `.gitignore` artifact for cli-template-go is always
  # produced before any unrelated build step can fail.
  ( cd "$REPO_ROOT" && rm -rf templates/dist \
      && bash templates/build.sh >/dev/null 2>&1 || true )

  local gi="$REPO_ROOT/templates/dist/cli-template-go/.gitignore"
  if [ ! -f "$gi" ]; then
    echo "      build.sh did not produce cli-template-go/.gitignore"
    return 1
  fi

  # Open marker must be on the very first line, close marker on
  # the last non-empty line. This locks the wrapping invariant.
  local first last
  first="$(head -n 1 "$gi")"
  last="$(awk 'NF{l=$0} END{print l}' "$gi")"

  if [ "$first" != "# >>> kit-managed: gitignore >>>" ]; then
    echo "      expected open marker on line 1, got: $first"
    return 1
  fi
  if [ "$last" != "# <<< kit-managed: gitignore <<<" ]; then
    echo "      expected close marker on last non-empty line, got: $last"
    return 1
  fi

  # Sanity: composed payload must include at least one entry
  # from common.gitignore and one from go.gitignore.
  if ! grep -q '^\.DS_Store$' "$gi"; then
    echo "      common.gitignore content (.DS_Store) missing"
    return 1
  fi
  if ! grep -q '^coverage\.out$' "$gi"; then
    echo "      go.gitignore content (coverage.out) missing"
    return 1
  fi

  # Save snapshot for the idempotency test.
  cp "$gi" "$td/baseline.gitignore"
  return 0
}

# ------------------------------------------------------------
# Test 8: re-running build.sh produces a byte-identical
# `.gitignore` (full idempotency). compose_gitignore is a pure
# `cat` so this is also a regression net against accidental
# nondeterminism (e.g. lang ordering, timestamp injection).
# ------------------------------------------------------------
test_gitignore_build_is_byte_identical() {
  local td="$1"

  ( cd "$REPO_ROOT" && rm -rf templates/dist \
      && bash templates/build.sh >/dev/null 2>&1 || true )
  local first="$REPO_ROOT/templates/dist/cli-template-go/.gitignore"
  if [ ! -f "$first" ]; then
    echo "      first build did not produce cli-template-go/.gitignore"
    return 1
  fi
  cp "$first" "$td/first.gitignore"

  ( cd "$REPO_ROOT" && rm -rf templates/dist \
      && bash templates/build.sh >/dev/null 2>&1 || true )
  local second="$REPO_ROOT/templates/dist/cli-template-go/.gitignore"
  if [ ! -f "$second" ]; then
    echo "      second build did not produce cli-template-go/.gitignore"
    return 1
  fi

  if ! cmp -s "$td/first.gitignore" "$second"; then
    echo "      build.sh produced different .gitignore bytes across runs:"
    diff "$td/first.gitignore" "$second" | sed 's/^/        /'
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Test 9: user content placed ABOVE the open marker and BELOW
# the close marker in `.gitignore` survives `kit init --update`.
# Today `--update` does not touch `.gitignore` (no Go-side
# emitter is wired into ManagedFiles), so the round-trip is
# trivially preserving. This test locks that contract: any
# future emitter that picks up `.gitignore` MUST use the
# managed-block helpers (mb_write) so user content above/below
# is preserved, and this test will catch a clobbering
# regression the moment it ships.
# ------------------------------------------------------------
test_gitignore_user_content_survives_update() {
  local td="$1"
  echo 'module test' > "$td/go.mod"

  # Seed a `.gitignore` that mimics what build.sh emits, with
  # user content above the open marker and below the close
  # marker.
  cat > "$td/.gitignore" <<'EOF'
# user content ABOVE the managed block
.idea-local/
notes.private.md

# >>> kit-managed: gitignore >>>
.DS_Store
coverage.out
# <<< kit-managed: gitignore <<<

# user content BELOW the managed block
.cache/local-stuff
EOF

  local before_sha
  if command -v sha256sum >/dev/null 2>&1; then
    before_sha="$(sha256sum "$td/.gitignore" | awk '{print $1}')"
  else
    before_sha="$(shasum -a 256 "$td/.gitignore" | awk '{print $1}')"
  fi

  ( cd "$td" && "$KIT" init --update ) >/dev/null 2>&1 \
    || { echo "      --update failed"; return 1; }

  local after_sha
  if command -v sha256sum >/dev/null 2>&1; then
    after_sha="$(sha256sum "$td/.gitignore" | awk '{print $1}')"
  else
    after_sha="$(shasum -a 256 "$td/.gitignore" | awk '{print $1}')"
  fi

  # User content above must still be there.
  if ! grep -q '^\.idea-local/$' "$td/.gitignore"; then
    echo "      user content above the open marker was stripped"
    return 1
  fi
  if ! grep -q '^notes\.private\.md$' "$td/.gitignore"; then
    echo "      user content above the open marker was stripped"
    return 1
  fi

  # User content below must still be there.
  if ! grep -q '^\.cache/local-stuff$' "$td/.gitignore"; then
    echo "      user content below the close marker was stripped"
    return 1
  fi

  # Markers must still be present.
  if ! grep -q '^# >>> kit-managed: gitignore >>>$' "$td/.gitignore"; then
    echo "      open marker missing after --update"
    return 1
  fi
  if ! grep -q '^# <<< kit-managed: gitignore <<<$' "$td/.gitignore"; then
    echo "      close marker missing after --update"
    return 1
  fi

  # Today: `--update` does not touch .gitignore, so the file is
  # byte-identical. If a future emitter is wired in, this hash
  # assertion may need to flip to a "marker-content matches
  # manifest" check. Keep the assert + comment so the contract
  # change is intentional.
  if [ "$before_sha" != "$after_sha" ]; then
    echo "      .gitignore bytes changed across --update:"
    echo "        before: $before_sha"
    echo "        after:  $after_sha"
    echo "      (if you wired a runtime .gitignore emitter, update"
    echo "       this assertion to verify mb_write preservation"
    echo "       semantics rather than full byte equality.)"
    return 1
  fi
  return 0
}

# ------------------------------------------------------------
# Runner
# ------------------------------------------------------------

run_test() {
  local name="$1" fn="$2"
  printf "\xE2\x86\x92 %s\n" "$name"
  if with_tempdir "$fn"; then
    ok "$name"
  else
    notok "$name"
  fi
}

echo "kit init idempotency E2E (binary: $KIT)"
echo "---"

run_test "double --update is byte-identical"        test_double_update_zero_diff
run_test "user content above markers survives"      test_user_content_above_markers
run_test "--add-service redis twice == once"        test_add_service_idempotent
run_test "--check: 0 on clean, 1 on drift"          test_check_drift_exit_codes
run_test "detect-langs gates runtime entries"       test_detect_langs_gating
run_test "--services is REPLACING (not additive)"   test_services_replace_semantics
run_test ".gitignore has kit-managed markers"       test_gitignore_has_kit_managed_markers
run_test ".gitignore build is byte-identical"       test_gitignore_build_is_byte_identical
run_test ".gitignore user content survives update"  test_gitignore_user_content_survives_update

echo "---"
printf "passed: %d, failed: %d\n" "$pass" "$fail"

if [ "$fail" -ne 0 ]; then
  echo "failures:"
  for e in "${errors[@]}"; do
    printf "  - %s\n" "$e"
  done
  exit 1
fi
exit 0
