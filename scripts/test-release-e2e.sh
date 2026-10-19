#!/usr/bin/env bash
set -euo pipefail

# -------------------------------------------------------
# test-release-e2e.sh — E2e tests for the release promotion wrapper
# and changelog rewrite scripts.
# -------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_BASE="$(mktemp -d)"
PASS=0
FAIL=0
ERRORS=()

cleanup() { rm -rf "$TMPDIR_BASE"; }
trap cleanup EXIT

# --- Helpers -------------------------------------------

info()  { printf "\033[1;34m[INFO]\033[0m  %s\n" "$*"; }
pass()  { printf "\033[1;32m[PASS]\033[0m  %s\n" "$*"; PASS=$((PASS + 1)); }
fail()  { printf "\033[1;31m[FAIL]\033[0m  %s\n" "$*"; FAIL=$((FAIL + 1)); ERRORS+=("$*"); }

assert_file_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    pass "$label"
  else
    fail "$label: '$pattern' not found in $file"
  fi
}

assert_file_not_contains() {
  local file="$1" pattern="$2" label="$3"
  if grep -q "$pattern" "$file" 2>/dev/null; then
    fail "$label: '$pattern' unexpectedly found in $file"
  else
    pass "$label"
  fi
}

assert_eq() {
  local got="$1" want="$2" label="$3"
  if [ "$got" = "$want" ]; then
    pass "$label"
  else
    fail "$label: got '$got', want '$want'"
  fi
}

assert_exit_code() {
  local expected="$1" actual="$2" label="$3"
  if [ "$actual" -eq "$expected" ]; then
    pass "$label"
  else
    fail "$label: expected exit $expected, got $actual"
  fi
}

# Create a temp git repo with a CHANGELOG.md
# $1 = dir name, $2 = changelog content
make_changelog_repo() {
  local dir="$TMPDIR_BASE/$1"
  mkdir -p "$dir"
  git -C "$dir" init -q
  git -C "$dir" config user.name "Test"
  git -C "$dir" config user.email "test@test.com"
  git -C "$dir" remote add origin "https://github.com/hop-top/kit.git"

  printf '%s' "$2" > "$dir/CHANGELOG.md"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m "init"
  echo "$dir"
}

PROMOTE="$SCRIPT_DIR/promote-release.sh"
REWRITE="$SCRIPT_DIR/rewrite-changelog.sh"

# ======================================================
# promote-release.sh tests
# ======================================================
#
# promote-release.sh hands its arguments to hop-top/.github's promote.js and
# commits the config change; the promotion rules are that tool's (and its
# tests'). These cover the wrapper: the pin it reads, the pass-through, the
# exit status, the commit and its refusals. A stub extension directory and a
# stub NODE keep them offline; every case that may commit runs on a copy of
# the wrapper in a throwaway repo, never on this one.

STUB_EXT="$TMPDIR_BASE/stub-ext"
STUB_NODE="$TMPDIR_BASE/stub-node"
STUB_LOG="$TMPDIR_BASE/stub-node.log"
mkdir -p "$STUB_EXT/bin"
echo "// stub" > "$STUB_EXT/bin/promote.js"
cat > "$STUB_NODE" <<'EOSTUB'
#!/usr/bin/env bash
# Records the working directory and every argument, one per line. With
# STUB_WRITE (and no --dry-run) it changes the config promote.js would write
# and prints a report line.
{ pwd; printf '%s\n' "$@"; } > "$STUB_LOG"
[ "${STUB_EXIT:-0}" -eq 0 ] || { echo "refused: stub" >&2; exit "$STUB_EXIT"; }
case " $* " in *" --dry-run "*) exit 0 ;; esac
if [ -n "${STUB_WRITE:-}" ]; then
  printf '{"promoted": "%s"}\n' "$STUB_WRITE" > .github/release-please-config.json
  echo "kit (.): x-channel $STUB_WRITE"
fi
EOSTUB
chmod +x "$STUB_NODE"
export STUB_LOG

# make_wrapper_repo <name> [release-please.yml body]: a throwaway repo with a
# copy of the wrapper, a release-please.yml and a committed config.
make_wrapper_repo() {
  local dir="$TMPDIR_BASE/$1"
  mkdir -p "$dir/scripts" "$dir/.github/workflows"
  git -C "$dir" init -q
  git -C "$dir" config user.name "Test"
  git -C "$dir" config user.email "test@test.com"
  cp "$PROMOTE" "$dir/scripts/promote-release.sh"
  printf '%s\n' "${2:-    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v0}" \
    > "$dir/.github/workflows/release-please.yml"
  echo '{}' > "$dir/.github/release-please-config.json"
  echo base > "$dir/README.md"
  git -C "$dir" add -A
  git -C "$dir" commit -q -m init
  echo "$dir"
}

# promote <repo> <args...>: the repo's wrapper copy with the stubs.
promote() {
  local dir="$1"
  shift
  RELEASE_PLEASE_TARGET_DIR="$STUB_EXT" NODE="$STUB_NODE" \
    bash "$dir/scripts/promote-release.sh" "$@"
}

commits() { git -C "$1" rev-list --count HEAD; }

info "Test p1: --pin prints the ref release-please.yml calls the extension at"
want=$(sed -n 's|.*release-please-on-push\.yml@\([0-9a-f]*\).*|\1|p' \
  "$SCRIPT_DIR/../.github/workflows/release-please.yml")
got=$(bash "$PROMOTE" --pin)
assert_eq "$got" "$want" "p1: pin as release-please.yml has it"
if [[ "$got" =~ ^[0-9a-f]{40}$ ]]; then
  pass "p1: pin is a full commit SHA"
else
  fail "p1: pin '$got' is not a full commit SHA"
fi

info "Test p2: --dry-run: arguments reach promote.js unchanged, from the repo root; no commit"
DIR=$(make_wrapper_repo t2)
rc=0
(cd "$TMPDIR_BASE" && STUB_WRITE=beta promote "$DIR" \
  channel beta --package kit --package 'a b' --dry-run) || rc=$?
assert_exit_code 0 "$rc" "p2: exit 0"
expected=$(printf '%s\n' "$DIR" "$STUB_EXT/bin/promote.js" \
  channel beta --package kit --package 'a b' --dry-run)
assert_eq "$(cat "$STUB_LOG")" "$expected" "p2: argv and cwd as given"
assert_eq "$(commits "$DIR")" 1 "p2: nothing committed"

info "Test p3: promote.js's exit status is the wrapper's; nothing committed"
DIR=$(make_wrapper_repo t3)
for code in 1 2; do
  rc=0
  STUB_EXIT=$code STUB_WRITE=rc promote "$DIR" channel rc --group kit 2>/dev/null || rc=$?
  assert_exit_code "$code" "$rc" "p3: exit $code passed through"
done
assert_eq "$(commits "$DIR")" 1 "p3: nothing committed"

info "Test p4: an extension directory without promote.js fails setup"
DIR=$(make_wrapper_repo t4)
rc=0
out=$(RELEASE_PLEASE_TARGET_DIR="$TMPDIR_BASE/nowhere" NODE="$STUB_NODE" \
  bash "$DIR/scripts/promote-release.sh" channel beta --package kit 2>&1) || rc=$?
assert_exit_code 3 "$rc" "p4: exit 3"
if echo "$out" | grep -q 'nowhere/bin/promote.js not found'; then
  pass "p4: names the missing file"
else
  fail "p4: message: $out"
fi

info "Test p5: a missing, doubled or malformed pin fails setup"
DIR=$(make_wrapper_repo t5a "name: release-please")
rc=0; out=$(bash "$DIR/scripts/promote-release.sh" --pin 2>&1) || rc=$?
assert_exit_code 3 "$rc" "p5a: no release-please-on-push call"
assert_eq "${out#*: }" "no hop-top/.github release-please-on-push.yml call in $DIR/.github/workflows/release-please.yml" "p5a: message"
DIR=$(make_wrapper_repo t5b "$(printf '%s\n' \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v0' \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@v1')")
rc=0; out=$(bash "$DIR/scripts/promote-release.sh" --pin 2>&1) || rc=$?
assert_exit_code 3 "$rc" "p5b: two calls"
assert_eq "${out#*: }" "more than one release-please-on-push.yml call in $DIR/.github/workflows/release-please.yml" "p5b: message"
# shellcheck disable=SC2016 # the literal `$(id)` is the malformed ref
DIR=$(make_wrapper_repo t5c \
  '    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@$(id)')
rc=0; bash "$DIR/scripts/promote-release.sh" --pin 2>/dev/null || rc=$?
assert_exit_code 3 "$rc" "p5c: not a ref"
SHA40=0123456789abcdef0123456789abcdef01234567
DIR=$(make_wrapper_repo t5d \
  "    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@$SHA40  # pin")
got=$(bash "$DIR/scripts/promote-release.sh" --pin)
assert_eq "$got" "$SHA40" "p5d: trailing comment ignored"
for ref in v0 "${SHA40:0:7}" main 0123456789ABCDEF0123456789ABCDEF01234567; do
  DIR=$(make_wrapper_repo "t5e-$ref" \
    "    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@$ref")
  rc=0; out=$(bash "$DIR/scripts/promote-release.sh" --pin 2>&1) || rc=$?
  assert_exit_code 3 "$rc" "p5e: pin $ref refused"
  assert_eq "${out#*: }" \
    "pinned ref '$ref' is not a full commit SHA: $DIR/.github/workflows/release-please.yml must call release-please-on-push.yml at a 40-character commit SHA" \
    "p5e: pin $ref: message"
done

info "Test p6: a promotion is committed alone, as a chore(release) commit"
DIR=$(make_wrapper_repo t6)
echo "unrelated" >> "$DIR/README.md"
echo "new" > "$DIR/untracked.txt"
rc=0
(cd "$TMPDIR_BASE" && STUB_WRITE=beta promote "$DIR" \
  channel beta --package kit --package qmochi >/dev/null) || rc=$?
assert_exit_code 0 "$rc" "p6: exit 0"
assert_eq "$(commits "$DIR")" 2 "p6: one commit"
assert_eq "$(git -C "$DIR" log -1 --format=%s)" \
  "chore(release): promote kit and qmochi to beta" "p6: subject"
assert_eq "$(git -C "$DIR" log -1 --format=%b)" "kit (.): x-channel beta" "p6: body is promote.js's report"
assert_eq "$(git -C "$DIR" show --name-only --format= HEAD)" \
  ".github/release-please-config.json" "p6: only the config committed"
assert_eq "$(git -C "$DIR" status --porcelain)" "$(printf ' M README.md\n?? untracked.txt')" \
  "p6: other changes left as they were"

info "Test p7: subjects for groups, opened lines and cleared records"
DIR=$(make_wrapper_repo t7)
STUB_WRITE=rc promote "$DIR" channel rc --group kit --package qmochi >/dev/null
assert_eq "$(git -C "$DIR" log -1 --format=%s)" \
  "chore(release): promote group kit and qmochi to rc" "p7: names in argument order"
STUB_WRITE=open promote "$DIR" open-line 1.0.0 --group=kit >/dev/null
assert_eq "$(git -C "$DIR" log -1 --format=%s)" \
  "chore(release): open 1.0.0 for group kit" "p7: open-line"
STUB_WRITE=clear promote "$DIR" clear channel -p a -p b -p c >/dev/null
assert_eq "$(git -C "$DIR" log -1 --format=%s)" \
  "chore(release): clear channel for a, b and c" "p7: clear, three packages"
assert_eq "$(commits "$DIR")" 4 "p7: one commit each"

info "Test p8: staged changes refuse the run; --dry-run ignores them"
DIR=$(make_wrapper_repo t8)
echo "staged" > "$DIR/staged.txt"
git -C "$DIR" add staged.txt
: > "$STUB_LOG"
rc=0; out=$(STUB_WRITE=beta promote "$DIR" channel beta --package kit 2>&1) || rc=$?
assert_exit_code 1 "$rc" "p8: refused"
if echo "$out" | grep -q 'staged changes detected'; then
  pass "p8: says why"
else
  fail "p8: message: $out"
fi
assert_eq "$(cat "$STUB_LOG")" "" "p8: promote.js not run"
assert_eq "$(commits "$DIR")" 1 "p8: nothing committed"
rc=0; STUB_WRITE=beta promote "$DIR" channel beta --package kit --dry-run >/dev/null || rc=$?
assert_exit_code 0 "$rc" "p8: --dry-run with a staged file runs"
assert_eq "$(commits "$DIR")" 1 "p8: --dry-run commits nothing"

info "Test p9: uncommitted changes in the config refuse the run"
DIR=$(make_wrapper_repo t9)
echo '{"hand": "edit"}' > "$DIR/.github/release-please-config.json"
rc=0; STUB_WRITE=beta promote "$DIR" channel beta --package kit 2>/dev/null || rc=$?
assert_exit_code 1 "$rc" "p9: refused"
assert_eq "$(commits "$DIR")" 1 "p9: nothing committed"

info "Test p10: --no-commit writes without committing and is not passed on"
DIR=$(make_wrapper_repo t10)
rc=0; STUB_WRITE=beta promote "$DIR" channel beta --package kit --no-commit >/dev/null || rc=$?
assert_exit_code 0 "$rc" "p10: exit 0"
assert_eq "$(commits "$DIR")" 1 "p10: nothing committed"
assert_eq "$(git -C "$DIR" status --porcelain)" " M .github/release-please-config.json" "p10: config written"
if grep -qx -- '--no-commit' "$STUB_LOG"; then
  fail "p10: --no-commit reached promote.js"
else
  pass "p10: --no-commit kept from promote.js"
fi

info "Test p11: nothing written, nothing committed"
DIR=$(make_wrapper_repo t11)
rc=0; promote "$DIR" clear channel --package kit >/dev/null || rc=$?
assert_exit_code 0 "$rc" "p11: exit 0"
assert_eq "$(commits "$DIR")" 1 "p11: no empty commit"

# Fetching the extension, offline: a global git config rewrites
# https://github.com/hop-top/.github.git to a local repository, and a stub
# npm stands in for `npm ci`. With STUB_NPM_RACE the stub plays a run that
# finished first: it puts a complete copy at the final cache path.
SRC="$TMPDIR_BASE/dotgithub"
mkdir -p "$SRC/scripts/release-please-target/bin"
git -C "$SRC" init -q
echo "// fetched" > "$SRC/scripts/release-please-target/bin/promote.js"
echo "outside the sparse checkout" > "$SRC/README.md"
git -C "$SRC" add -A
git -C "$SRC" -c user.name=Test -c user.email=test@test.com commit -q -m init
SRC_SHA=$(git -C "$SRC" rev-parse HEAD)
FETCH_GITCONFIG="$TMPDIR_BASE/fetch.gitconfig"
printf '[url "file://%s"]\n\tinsteadOf = https://github.com/hop-top/.github.git\n' "$SRC" > "$FETCH_GITCONFIG"
STUB_BIN="$TMPDIR_BASE/stub-bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/npm" <<'EOSTUB'
#!/usr/bin/env bash
[ -f package.json ] || [ -f bin/promote.js ] || exit 1
if [ -n "${STUB_NPM_RACE:-}" ]; then
  final=${PWD%/scripts/release-please-target}
  final=${final%.*}
  mkdir -p "$final/scripts/release-please-target/bin"
  echo "// first run" > "$final/scripts/release-please-target/bin/promote.js"
fi
EOSTUB
chmod +x "$STUB_BIN/npm"

# entries <dir>: its entries' names, sorted, space-separated.
entries() { find "$1" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort | tr '\n' ' '; }

# fetch_promote <cache home> <repo name>: a --dry-run through the real fetch path.
fetch_promote() {
  local dir
  dir=$(make_wrapper_repo "$2" \
    "    uses: hop-top/.github/.github/workflows/release-please-on-push.yml@$SRC_SHA")
  RELEASE_PLEASE_TARGET_DIR='' GIT_CONFIG_GLOBAL="$FETCH_GITCONFIG" XDG_CACHE_HOME="$1" PATH="$STUB_BIN:$PATH" \
    NODE="$STUB_NODE" bash "$dir/scripts/promote-release.sh" channel beta --package kit --dry-run
}

info "Test p12: first use fetches the pinned commit into the cache; other pins' caches go"
CACHE="$TMPDIR_BASE/cache12"
ROOT="$CACHE/poly-kit/release-please-target"
OLD_PIN=fedcba9876543210fedcba9876543210fedcba98
mkdir -p "$ROOT/$OLD_PIN/scripts" "$ROOT/$SRC_SHA.inflight" "$ROOT/notes"
rc=0; fetch_promote "$CACHE" t12 2>/dev/null || rc=$?
assert_exit_code 0 "$rc" "p12: exit 0"
assert_eq "$(sed -n 2p "$STUB_LOG")" "$ROOT/$SRC_SHA/scripts/release-please-target/bin/promote.js" \
  "p12: promote.js run from the cache"
assert_eq "$(cat "$ROOT/$SRC_SHA/scripts/release-please-target/bin/promote.js")" "// fetched" \
  "p12: cache holds the pinned commit"
assert_eq "$(find "$ROOT/$SRC_SHA" -name README.md)" "" "p12: sparse: only scripts/release-please-target"
assert_eq "$(entries "$ROOT")" "$SRC_SHA $SRC_SHA.inflight notes " \
  "p12: another pin's cache removed; temporary and foreign entries kept"

info "Test p13: a run that finished first keeps its copy; nothing nested, nothing left over"
CACHE="$TMPDIR_BASE/cache13"
ROOT="$CACHE/poly-kit/release-please-target"
rc=0; STUB_NPM_RACE=1 fetch_promote "$CACHE" t13 2>/dev/null || rc=$?
assert_exit_code 0 "$rc" "p13: exit 0"
assert_eq "$(entries "$ROOT")" "$SRC_SHA " "p13: no temporary copy left beside it"
assert_eq "$(entries "$ROOT/$SRC_SHA")" "scripts " "p13: nothing moved inside it"
assert_eq "$(cat "$ROOT/$SRC_SHA/scripts/release-please-target/bin/promote.js")" "// first run" \
  "p13: the first run's copy kept"

# ======================================================
# rewrite-changelog.sh tests
# ======================================================

RAW_CHANGELOG='# Changelog

## [0.4.0](https://github.com/hop-top/kit/compare/kit-v0.3.2...kit-v0.4.0) (2026-04-18)

### Features

* **bus:** pluggable adapter (abc1234)
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46))

### Bug Fixes

* **output:** fix rendering (def5678)
'

FEATURES_ONLY='# Changelog

## [1.0.0](https://github.com/hop-top/kit/compare/kit-v0.9.0...kit-v1.0.0) (2026-04-18)

### Features

* **core:** new thing (aaa1111)
'

FIXES_ONLY='# Changelog

## [1.0.1](https://github.com/hop-top/kit/compare/kit-v1.0.0...kit-v1.0.1) (2026-04-18)

### Bug Fixes

* **core:** patch (bbb2222)
'

TWO_ENTRIES='# Changelog

## [0.5.0](https://github.com/hop-top/kit/compare/kit-v0.4.0...kit-v0.5.0) (2026-04-18)

### Features

* **new:** thing (ccc3333)

## [0.4.0](https://github.com/hop-top/kit/compare/kit-v0.3.2...kit-v0.4.0) (2026-04-17)

### Bug Fixes

* **old:** preserved entry (ddd4444)
'

# Mock gh by ensuring GH_TOKEN is unset and gh api fails gracefully
export GH_TOKEN=""

info "Test 9: Basic rewrite"
DIR=$(make_changelog_repo "t9" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "team is happy" "t9: intro paragraph present"
assert_file_contains "$DIR/CHANGELOG.md" \
  'pluggable adapter (abc1234)$' "t9: commit SHA kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'scaffolder (\[#46\](https://github.com/hop-top/kit/pull/46))$' "t9: PR link kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  "Full diff" "t9: full diff link"

info "Test 10: Idempotent"
DIR=$(make_changelog_repo "t10" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
cp "$DIR/CHANGELOG.md" "$DIR/first.md"
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
if diff -q "$DIR/first.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t10: idempotent — second run unchanged"
else
  fail "t10: idempotent — output differs on second run"
fi

info "Test 11: Old entries preserved"
DIR=$(make_changelog_repo "t11" "$TWO_ENTRIES")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "team is happy" "t11: latest entry rewritten"
assert_file_contains "$DIR/CHANGELOG.md" \
  '(ddd4444)' "t11: old entry SHA preserved"
assert_file_contains "$DIR/CHANGELOG.md" \
  'preserved entry' "t11: old entry text intact"

info "Test 12: Release type detection — features only"
DIR=$(make_changelog_repo "t12a" "$FEATURES_ONLY")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "new features" "t12a: features-only → new features"

info "Test 12: Release type detection — fixes only"
DIR=$(make_changelog_repo "t12b" "$FIXES_ONLY")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "maintenance release with bug fixes" "t12b: fixes-only → maintenance"

info "Test 12: Release type detection — both"
DIR=$(make_changelog_repo "t12c" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  "new features and bug fixes" "t12c: both → features and fixes"

info "Test 13: Already rewritten (marker present)"
DIR=$(make_changelog_repo "t13" "$RAW_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
cp "$DIR/CHANGELOG.md" "$DIR/before.md"
rc=0
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit || rc=$?
assert_exit_code 0 "$rc" "t13: already rewritten exits 0"
if diff -q "$DIR/before.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t13: no changes on already-rewritten file"
else
  fail "t13: file modified despite marker"
fi

# ======================================================
# --dry-run tests
# ======================================================

info "Test 18: rewrite --dry-run doesn't modify changelog"
DIR=$(make_changelog_repo "t18" "$RAW_CHANGELOG")
cp "$DIR/CHANGELOG.md" "$DIR/before.md"
bash "$REWRITE" --dry-run --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
if diff -q "$DIR/before.md" "$DIR/CHANGELOG.md" >/dev/null 2>&1; then
  pass "t18: changelog unchanged after --dry-run"
else
  fail "t18: changelog modified despite --dry-run"
fi

info "Test 19: rewrite --dry-run shows diff"
DIR=$(make_changelog_repo "t19" "$RAW_CHANGELOG")
out=$(bash "$REWRITE" --dry-run --file "$DIR/CHANGELOG.md" \
  --component kit --repo hop-top/kit 2>&1)
echo "$out" | grep -q '\[dry-run\] Would rewrite' \
  && pass "t19a: shows would-rewrite" \
  || fail "t19a: missing would-rewrite"
echo "$out" | grep -q '\[dry-run\] Version: 0.4.0' \
  && pass "t19b: shows version" \
  || fail "t19b: missing version"
echo "$out" | grep -q '\[dry-run\] Release type:' \
  && pass "t19c: shows release type" \
  || fail "t19c: missing release type"
echo "$out" | grep -q '\[dry-run\] No changes made' \
  && pass "t19d: shows no-changes notice" \
  || fail "t19d: missing no-changes notice"
echo "$out" | grep -q '^---\|^+++\|^@@' \
  && pass "t19e: shows unified diff" \
  || fail "t19e: missing diff output"

# ======================================================
# Linked SHA and PR link tests
# ======================================================

LINKED_SHA_CHANGELOG='# Changelog

## [0.6.0](https://github.com/hop-top/kit/compare/kit-v0.5.0...kit-v0.6.0) (2026-04-18)

### Features

* **bus:** pluggable adapter ([abc1234](https://github.com/hop-top/kit/commit/abc1234))
* **cli:** scaffolder ([#46](https://github.com/hop-top/kit/pull/46)) ([def5678](https://github.com/hop-top/kit/commit/def5678))

### Bug Fixes

* **core:** raw sha (aaa1111)
'

info "Test 20: Linked SHAs and PR links kept"
DIR=$(make_changelog_repo "t20" "$LINKED_SHA_CHANGELOG")
bash "$REWRITE" --file "$DIR/CHANGELOG.md" --component kit \
  --repo hop-top/kit
assert_file_contains "$DIR/CHANGELOG.md" \
  'pluggable adapter (\[abc1234\](https://github.com/hop-top/kit/commit/abc1234))$' \
  "t20a: linked SHA kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'scaffolder (\[#46\](https://github.com/hop-top/kit/pull/46)) (\[def5678\](https://github.com/hop-top/kit/commit/def5678))$' \
  "t20b: combined PR+SHA links kept"
assert_file_contains "$DIR/CHANGELOG.md" \
  'raw sha (aaa1111)$' "t20c: raw SHA kept"

# ======================================================
# Summary
# ======================================================

echo ""
echo "========================================"
printf "  Results: \033[1;32m%d passed\033[0m" "$PASS"
if [ "$FAIL" -gt 0 ]; then
  printf ", \033[1;31m%d failed\033[0m" "$FAIL"
fi
echo ""
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "Failures:"
  for err in "${ERRORS[@]}"; do
    printf "  - %s\n" "$err"
  done
  exit 1
fi
