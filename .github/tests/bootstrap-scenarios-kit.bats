#!/usr/bin/env bats
# Smoke tests for scripts/bootstrap-scenarios-kit.sh.
#
# Validates:
#   - first run on an empty target creates the expected layout
#   - second run on the same target is a no-op (idempotent)
#   - run against a non-empty target without our marker is refused
#   - templated content passes `kit conformance verify-no-leak`
#   - the verify-tier1.sh guardrail accepts the public workflow
#     and rejects a tier-2 / tier-3 reference
#
# Run: bats .github/tests/bootstrap-scenarios-kit.bats

BOOTSTRAP="scripts/bootstrap-scenarios-kit.sh"
VERIFY_TIER1="scripts/verify-tier1.sh"
DOGFOOD_WF=".github/workflows/dogfood-grade.yml"

setup() {
    TMP="$(mktemp -d)"
    cd "$BATS_TEST_DIRNAME/../.."
    REPO_ROOT="$(pwd)"
}

teardown() {
    rm -rf "$TMP"
}

# ── Bootstrap script ──────────────────────────────────────────────

@test "bootstrap creates expected layout on first run" {
    run "$REPO_ROOT/$BOOTSTRAP" "$TMP/target"
    [ "$status" -eq 0 ]
    [ -f "$TMP/target/README.md" ]
    [ -f "$TMP/target/LICENSE" ]
    [ -f "$TMP/target/.verifynoleak.allow" ]
    [ -f "$TMP/target/scenarios/spaced/launch-dry-run-no-mutation.yaml" ]
    [ -f "$TMP/target/.github/workflows/grade-on-dispatch.yml" ]
    [ -f "$TMP/target/.github/workflows/grade-on-pr.yml" ]
    [ -f "$TMP/target/.github/workflows/sync-rules.yml" ]
    [ -f "$TMP/target/contracts/scenario-rules.json" ]
}

@test "bootstrap initialises a git repo on first run" {
    run "$REPO_ROOT/$BOOTSTRAP" "$TMP/target"
    [ "$status" -eq 0 ]
    [ -d "$TMP/target/.git" ]
    cd "$TMP/target"
    run git log --oneline -1
    [ "$status" -eq 0 ]
    [[ "$output" == *"bootstrap from hop-top/kit"* ]]
}

@test "bootstrap is idempotent on re-run (no changes second time)" {
    "$REPO_ROOT/$BOOTSTRAP" "$TMP/target" >/dev/null
    run "$REPO_ROOT/$BOOTSTRAP" "$TMP/target"
    [ "$status" -eq 0 ]
    # No "wrote" lines on the second run: every template matched cmp -s.
    [[ "$output" != *"wrote $TMP/target/README.md"* ]]
    [[ "$output" != *"wrote $TMP/target/LICENSE"* ]]
    # Repo stays clean.
    cd "$TMP/target"
    run git status --porcelain
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "bootstrap refuses non-empty target without marker" {
    mkdir -p "$TMP/target"
    echo "unrelated" > "$TMP/target/random.txt"
    run "$REPO_ROOT/$BOOTSTRAP" "$TMP/target"
    [ "$status" -ne 0 ]
    [[ "$output" == *"refusing to bootstrap"* ]]
}

@test "bootstrap template passes verify-no-leak" {
    "$REPO_ROOT/$BOOTSTRAP" "$TMP/target" >/dev/null
    # Build the kit binary if not already present at /tmp/kit-dog-bin.
    bin="/tmp/kit-dog-bin"
    if [ ! -x "$bin" ]; then
        cd "$REPO_ROOT"
        go build -buildvcs=false -o "$bin" ./cmd/kit
    fi
    cd "$TMP/target"
    run "$bin" conformance verify-no-leak --paths=.
    [ "$status" -eq 0 ]
    [[ "$output" == *"0 findings"* ]]
}

# ── verify-tier1 guardrail ───────────────────────────────────────

@test "verify-tier1 accepts the public dogfood-grade workflow" {
    cd "$REPO_ROOT"
    run "$VERIFY_TIER1" "$DOGFOOD_WF"
    [ "$status" -eq 0 ]
    [[ "$output" == *"ok"* ]]
}

@test "verify-tier1 rejects a workflow that asks for tier 2" {
    cat > "$TMP/bad.yml" <<'YAML'
jobs:
  bad:
    steps:
      - run: kit conformance grade x.cassette y.yaml --tier=2
YAML
    cd "$REPO_ROOT"
    run "$VERIFY_TIER1" "$TMP/bad.yml"
    [ "$status" -ne 0 ]
}

@test "verify-tier1 rejects a workflow that asks for tier 3" {
    cat > "$TMP/bad.yml" <<'YAML'
jobs:
  bad:
    steps:
      - run: kit conformance grade x.cassette y.yaml --tier 3
YAML
    cd "$REPO_ROOT"
    run "$VERIFY_TIER1" "$TMP/bad.yml"
    [ "$status" -ne 0 ]
}

@test "verify-tier1 ignores tier-2 references in YAML comments" {
    cat > "$TMP/ok.yml" <<'YAML'
# we forbid --tier=2 and --tier=3 in this workflow
jobs:
  ok:
    steps:
      - run: kit conformance grade x.cassette y.yaml --tier=1
YAML
    cd "$REPO_ROOT"
    run "$VERIFY_TIER1" "$TMP/ok.yml"
    [ "$status" -eq 0 ]
}
