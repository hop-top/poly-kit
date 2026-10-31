#!/usr/bin/env bats
# Tests for scripts/toolchain-pins.sh and scripts/check-toolchain-parity.sh.
#
# Each test builds a throwaway git repo holding copies of both scripts and
# a minimal set of files that declare tool versions, all agreeing with its
# mise.toml, then breaks one declaration and asserts the exit code and the
# file:line reported.
#
# Run: bats .github/tests/toolchain-parity.bats
# Or:  make test-lint-scripts

SCRIPTS="$BATS_TEST_DIRNAME/../../scripts"

setup() {
    REPO="$(mktemp -d)"
    git -C "$REPO" init -q
    mkdir -p "$REPO/scripts"
    cp "$SCRIPTS/toolchain-pins.sh" "$SCRIPTS/check-toolchain-parity.sh" "$REPO/scripts/"

    write mise.toml '# header
# >>> kit-managed >>>
[tools]
go = "1.26.1"
node = "22.23.2"
pnpm = "11.4.0"
python = "3.13.5"
uv = "0.12.3"
rust = "1.98.0"
golangci-lint = "2.11.4"
ruff = "0.15.11"
lychee = "0.24.2"
"npm:release-please" = "17.1.0"

[env]
_.file = ".env"
# <<< kit-managed <<<'
    write .config/mise.toml '# repo-only tools
[tools]
buf = "1.73.0"
"npm:markdownlint-cli2" = "0.23.3"'
    write go.mod 'module example.com/m

go 1.26.1'
    write sub/go.mod 'module example.com/m/sub

go 1.25.0'
    write sdk/rs/rust-toolchain.toml '[toolchain]
channel = "1.98.0"'
    write sdk/py/uv.lock '[[package]]
name = "pytest"
version = "9.0.2"

[[package]]
name = "ruff"
version = "0.15.11"'
    write sdk/experimental/php/composer.json '{
    "require": {
        "php": "^8.4"
    }
}'
    write .github/workflows/ci.yml 'jobs:
  a:
    steps:
      - id: pins
        run: scripts/toolchain-pins.sh >> "$GITHUB_OUTPUT"
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ steps.pins.outputs.node }}
      - uses: pnpm/action-setup@v4
        with:
          version: 11.4.0
      - uses: lycheeverse/lychee-action@v2
        with:
          lycheeVersion: v${{ steps.pins.outputs.lychee }}
      - uses: bufbuild/buf-action@v1
        with:
          version: 1.73.0
      - uses: shivammathur/setup-php@v2
        with:
          php-version: "8.4"
      - uses: some/other-action@v1
        with:
          version: 9.9.9'
    write .devcontainer/flake.nix '          buildInputs = with pkgs; [
            go_1_26
            nodejs_22
            python313
            python313Packages.pip
          ];'
    write templates/shared/tool-versions.toml '# manifest
[runtimes]
go     = "1.26.1"  # Go toolchain
node   = "22.23.2" # Node.js
rust   = "1.98.0"

[workflow]
ruff                 = "0.15.11"  # Python linter
hadolint             = "2.12"     # only the manifest pins this
"npm:release-please" = "17.1.0"'
    mkdir -p "$REPO/cmd/kit/init/managed_assets"
    cp "$REPO/templates/shared/tool-versions.toml" "$REPO/cmd/kit/init/managed_assets/"
    git -C "$REPO" add -A
    write examples/app/ts/.nvmrc '22'
    write examples/app/py/.python-version '3.13'
    write .devcontainer/Dockerfile 'COPY --from=builder /nix/store/*-go-1.26*/bin/* /go/bin/
COPY --from=builder /nix/store/*-nodejs-22*/bin/* /node/bin/
ARG PYTHON_VERSION=3.13.5
COPY --from=ghcr.io/astral-sh/uv:0.12.3@sha256:0000 /uv /uvx /usr/local/bin/
ARG NODE_VERSION=22.23.2
ARG PNPM_VERSION=11.4.0'
}

teardown() {
    rm -rf "$REPO"
}

# write <path> <content>: write a file and stage it.
write() {
    mkdir -p "$(dirname "$REPO/$1")"
    printf '%s\n' "$2" > "$REPO/$1"
    git -C "$REPO" add -A
}

# edit <path> <sed-expr>: change a tracked file in place and restage it.
edit() {
    sed -i.bak -e "$2" "$REPO/$1"
    rm -f "$REPO/$1.bak"
    git -C "$REPO" add -A
}

# has / lacks <text>: assert on $output. A failed `[[ ]]` that is not a
# test's last command does not fail it under bash 3.2 (macOS /bin/bash),
# so the assertions live in functions, whose status set -e does see.
has() {
    case "$output" in *"$1"*) return 0 ;; esac
    printf 'expected output to contain: %s\n--- output ---\n%s\n' "$1" "$output" >&2
    return 1
}
lacks() {
    case "$output" in *"$1"*)
        printf 'expected output NOT to contain: %s\n--- output ---\n%s\n' "$1" "$output" >&2
        return 1 ;;
    esac
}

run_check() {
    run bash -c "cd '$REPO' && scripts/check-toolchain-parity.sh"
}

run_pins() {
    run bash -c "cd '$REPO' && scripts/toolchain-pins.sh $*"
}

# --- toolchain-pins.sh ----------------------------------------------------

@test "pins: prints every [tools] entry as name=version, backend prefix dropped" {
    run_pins
    [ "$status" -eq 0 ]
    has "go=1.26.1"
    has "markdownlint-cli2=0.23.3"
    lacks "npm:"
    lacks "_.file"
}

@test "pins: prints one tool's version" {
    run_pins rust
    [ "$status" -eq 0 ]
    [ "$output" = "1.98.0" ]
}

@test "pins: unknown tool exits 1" {
    run_pins nosuchtool
    [ "$status" -eq 1 ]
    has "not pinned"
}

@test "pins: a table value is refused, not skipped" {
    edit mise.toml 's/^rust = .*/rust = { version = "1.98.0" }/'
    run_pins
    [ "$status" -eq 2 ]
    has "rust"
}

# --- check-toolchain-parity.sh --------------------------------------------

@test "agreeing tree passes" {
    run_check
    [ "$status" -eq 0 ]
    has "agree with mise.toml"
}

@test "root go.mod directive differing from the go pin fails" {
    edit go.mod 's/^go 1.26.1$/go 1.26.2/'
    run_check
    [ "$status" -eq 1 ]
    has "go.mod:3: go (directive) 1.26.2 disagrees with mise.toml go 1.26.1"
}

@test "go pin differing from go.mod fails the same way" {
    edit mise.toml 's/^go = .*/go = "1.26"/'
    run_check
    [ "$status" -eq 1 ]
    has "go.mod:3:"
}

@test "nested go.mod newer than the go pin fails; older passes" {
    run_check
    [ "$status" -eq 0 ]
    edit sub/go.mod 's/^go 1.25.0$/go 1.27.0/'
    run_check
    [ "$status" -eq 1 ]
    has "sub/go.mod:3:"
}

@test "a toolchain directive newer than the go pin fails" {
    printf 'toolchain go1.27.1\n' >> "$REPO/sub/go.mod"
    git -C "$REPO" add -A
    run_check
    [ "$status" -eq 1 ]
    has "sub/go.mod:4: go (toolchain) 1.27.1"
}

@test "rust-toolchain.toml channel differing from the rust pin fails" {
    edit sdk/rs/rust-toolchain.toml 's/1.98.0/1.99.0/'
    run_check
    [ "$status" -eq 1 ]
    has "rust-toolchain.toml:2: rust 1.99.0"
}

@test "locked ruff differing from the ruff pin fails" {
    edit sdk/py/uv.lock 's/^version = "0.15.11"$/version = "0.15.12"/'
    run_check
    [ "$status" -eq 1 ]
    has "sdk/py/uv.lock:7: ruff 0.15.12"
}

@test "workflow node-version literal differing from the pin fails" {
    edit .github/workflows/ci.yml 's/node-version: .*/node-version: "20"/'
    run_check
    [ "$status" -eq 1 ]
    has ".github/workflows/ci.yml:8: node 20 disagrees with mise.toml node 22.23.2"
}

@test "pnpm/action-setup version differing from the pnpm pin fails" {
    edit .github/workflows/ci.yml 's/          version: 11.4.0/          version: 9/'
    run_check
    [ "$status" -eq 1 ]
    has "ci.yml:11: pnpm 9"
}

@test "buf-action version differing from the buf pin fails" {
    edit .github/workflows/ci.yml 's/version: 1.73.0/version: 1.72.0/'
    run_check
    [ "$status" -eq 1 ]
    has "ci.yml:17: buf 1.72.0"
}

@test "a version input of an unrelated action is ignored" {
    run_check
    [ "$status" -eq 0 ]
    lacks "9.9.9"
}

@test "a pin missing from mise.toml fails even when workflows read it" {
    edit mise.toml '/^lychee = /d'
    run_check
    [ "$status" -eq 1 ]
    has "no pin for lychee"
}

@test "php-version differing from composer.json require.php fails" {
    edit .github/workflows/ci.yml 's/php-version: "8.4"/php-version: "8.3"/'
    run_check
    [ "$status" -eq 1 ]
    has "php 8.3 disagrees with sdk/experimental/php/composer.json require.php 8.4"
}

@test "an allow marker on the line exempts a deliberate difference" {
    edit .github/workflows/ci.yml 's/php-version: "8.4"/php-version: "8.3"  # toolchain-parity: allow (floor test)/'
    run_check
    [ "$status" -eq 0 ]
}

@test "devcontainer flake is held to the pinned families" {
    edit .devcontainer/flake.nix 's/nodejs_22/nodejs_20/'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/flake.nix:3: node 20"
}

@test "devcontainer Dockerfile store paths are held to the pinned families" {
    edit .devcontainer/Dockerfile 's/nodejs-22/nodejs-20/'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/Dockerfile:2: node 20"
}

@test "devcontainer Dockerfile python is held to the exact pin" {
    edit .devcontainer/Dockerfile 's/PYTHON_VERSION=3.13.5/PYTHON_VERSION=3.13.4/'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/Dockerfile:3: python 3.13.4"
}

@test "devcontainer Dockerfile uv image is held to the exact pin" {
    edit .devcontainer/Dockerfile 's|astral-sh/uv:0.12.3|astral-sh/uv:0.12.2|'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/Dockerfile:4: uv 0.12.2"
}

@test "devcontainer Dockerfile node is held to the exact pin" {
    edit .devcontainer/Dockerfile 's/NODE_VERSION=22.23.2/NODE_VERSION=22.23.1/'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/Dockerfile:5: node 22.23.1"
}

@test "devcontainer Dockerfile pnpm is held to the exact pin" {
    edit .devcontainer/Dockerfile 's/PNPM_VERSION=11.4.0/PNPM_VERSION=11.3.0/'
    run_check
    [ "$status" -eq 1 ]
    has ".devcontainer/Dockerfile:6: pnpm 11.3.0"
}

@test "templates/ pins scaffolded projects and is out of scope" {
    write templates/cli-go/go.mod 'module x

go 1.30.0'
    write templates/shared/ci.yml '      - uses: actions/setup-node@v4
        with:
          node-version: "18"'
    run_check
    [ "$status" -eq 0 ]
}

@test "untracked files are out of scope" {
    mkdir -p "$REPO/scratch"
    printf 'module s\n\ngo 1.30.0\n' > "$REPO/scratch/go.mod"
    run_check
    [ "$status" -eq 0 ]
}

@test "unreadable mise.toml exits 2" {
    edit mise.toml 's/^node = .*/node = 22/'
    run_check
    [ "$status" -eq 2 ]
}

@test "scaffold manifest pin differing from mise.toml fails" {
    edit templates/shared/tool-versions.toml 's/^ruff  *= "0.15.11"/ruff = "0.8"/'
    run_check
    [ "$status" -eq 1 ]
    has "templates/shared/tool-versions.toml:8: ruff 0.8 disagrees with mise.toml ruff 0.15.11"
}

@test "scaffold manifest: quoted backend key is compared under its tool name" {
    edit templates/shared/tool-versions.toml 's/"npm:release-please" = "17.1.0"/"npm:release-please" = "16"/'
    run_check
    [ "$status" -eq 1 ]
    has "tool-versions.toml:10: release-please 16 disagrees with mise.toml release-please 17.1.0"
}

@test "stale embedded manifest copy for kit init fails" {
    edit cmd/kit/init/managed_assets/tool-versions.toml 's/^go     = "1.26.1"/go     = "1.26"/'
    run_check
    [ "$status" -eq 1 ]
    has "cmd/kit/init/managed_assets/tool-versions.toml:3: go 1.26 disagrees with mise.toml go 1.26.1"
}

@test "scaffold manifest: tools mise.toml does not pin are ignored" {
    run_check
    [ "$status" -eq 0 ]
    lacks "hadolint"
}

@test ".nvmrc naming another node major fails" {
    edit examples/app/ts/.nvmrc 's/22/20/'
    run_check
    [ "$status" -eq 1 ]
    has "examples/app/ts/.nvmrc:1: node 20 disagrees with mise.toml node 22.23.2 (or 22)"
}

@test ".nvmrc with the v-prefixed exact pin passes" {
    edit examples/app/ts/.nvmrc 's/22/v22.23.2/'
    run_check
    [ "$status" -eq 0 ]
}

@test ".nvmrc naming another release in the pinned major fails" {
    edit examples/app/ts/.nvmrc 's/22/22.20.0/'
    run_check
    [ "$status" -eq 1 ]
    has "examples/app/ts/.nvmrc:1: node 22.20.0 disagrees with mise.toml node 22.23.2 (or 22)"
}

@test ".python-version naming another python minor fails" {
    edit examples/app/py/.python-version 's/3.13/3.12.9/'
    run_check
    [ "$status" -eq 1 ]
    has "examples/app/py/.python-version:1: python 3.12.9"
}

@test ".python-version with the exact pin passes" {
    edit examples/app/py/.python-version 's/3.13/3.13.5/'
    run_check
    [ "$status" -eq 0 ]
}

@test ".python-version naming another patch in the pinned minor fails" {
    edit examples/app/py/.python-version 's/3.13/3.13.2/'
    run_check
    [ "$status" -eq 1 ]
    has "examples/app/py/.python-version:1: python 3.13.2 disagrees with mise.toml python 3.13.5 (or 3.13)"
}

# --- exact pins -------------------------------------------------------------

@test "a partial pin in mise.toml fails" {
    edit mise.toml 's/^uv = .*/uv = "0.12"/'
    run_check
    [ "$status" -eq 1 ]
    has "mise config: uv 0.12 is not an exact version"
}

@test "a partial pin in .config/mise.toml fails" {
    edit .config/mise.toml 's/^buf = .*/buf = "1"/'
    run_check
    [ "$status" -eq 1 ]
    has "mise config: buf 1 is not an exact version"
}

@test "a pre-release pin is exact" {
    edit .config/mise.toml 's/^buf = .*/buf = "1.74.0-rc.1"/'
    run_check
    lacks "not an exact version"
}

# --- repo-only tools in .config/mise.toml ---------------------------------

@test "pins: a repo-only tool is read from .config/mise.toml" {
    run_pins buf
    [ "$status" -eq 0 ]
    [ "$output" = "1.73.0" ]
    run_pins
    has "markdownlint-cli2=0.23.3"
}

@test "pins: .config/mise.toml is optional" {
    rm "$REPO/.config/mise.toml"
    run_pins go
    [ "$status" -eq 0 ]
    [ "$output" = "1.26.1" ]
}

@test "a tool pinned in both files at the same version passes" {
    printf 'go = "1.26.1"\n' >> "$REPO/.config/mise.toml"
    git -C "$REPO" add -A
    run_check
    [ "$status" -eq 0 ]
}

@test "a tool pinned in both files at different versions fails" {
    printf 'go = "1.27.0"\n' >> "$REPO/.config/mise.toml"
    git -C "$REPO" add -A
    run_pins
    [ "$status" -eq 3 ]
    has ".config/mise.toml:5: go 1.27.0 disagrees with mise.toml:4 1.26.1"
    run_check
    [ "$status" -eq 1 ]
    has "pin the same tool at different versions"
}

@test "a repo-only tool added to the scaffold manifest fails" {
    printf 'buf = "1.73.0"\n' >> "$REPO/templates/shared/tool-versions.toml"
    git -C "$REPO" add -A
    run_check
    [ "$status" -eq 1 ]
    has "templates/shared/tool-versions.toml:11: buf is pinned only for this repo"
}

# --- pin lookup reads its whole input --------------------------------------

# Looking a pin up by piping the list into an awk that exits on the first
# match let printf hit a closed pipe; under pipefail that failed the
# lookup, and under load it did so at random. Pins past a pipe buffer's
# worth of text make the closed pipe certain, not a race.
pad_pins() {
    local i
    for i in $(seq 1 4000); do
        printf 'filler-tool-%04d = "1.0.%d"\n' "$i" "$i"
    done >> "$REPO/.config/mise.toml"
    git -C "$REPO" add -A
}

@test "pins: a lookup ahead of a long pin list still succeeds" {
    pad_pins
    run_pins go
    [ "$status" -eq 0 ]
    [ "$output" = "1.26.1" ]
}

@test "agreeing tree with a long pin list passes" {
    pad_pins
    run_check
    [ "$status" -eq 0 ]
}
