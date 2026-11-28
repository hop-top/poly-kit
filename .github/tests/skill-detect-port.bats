#!/usr/bin/env bats
# Tests for skills/hop-top-kit/scripts/detect-port.sh.
#
# Each test builds a throwaway project tree and asserts stdout, stderr and
# the exit code. The script is also run under dash and shellcheck when they
# are installed, so a bashism or a lint finding fails here.
#
# Run: bats .github/tests/skill-detect-port.bats
# Or:  make test-bats

SCRIPT="$BATS_TEST_DIRNAME/../../skills/hop-top-kit/scripts/detect-port.sh"
BASE=https://github.com/hop-top

setup() {
    DIR="$(mktemp -d)"
}

teardown() {
    rm -rf "$DIR"
}

# write <name> <content>: write a file in the project tree.
write() {
    mkdir -p "$(dirname "$DIR/$1")"
    printf '%s\n' "$2" > "$DIR/$1"
}

detect() { run sh "$SCRIPT" "$@"; }

# line <port> <manifest> <kit> <skill> <mirror>: the expected output line.
line() {
    printf 'port=%s manifest=%s kit=%s skill=%s url=%s/%s/blob/main/skills/%s/SKILL.md' \
        "$1" "$2" "$3" "$4" "$BASE" "$5" "$4"
}

@test "go.mod requiring kit reports the version" {
    write go.mod $'module example.com/app\n\ngo 1.27\n\nrequire hop.top/kit v0.5.0-beta.16'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line go go.mod v0.5.0-beta.16 hop-top-kit-go kit)" ]
}

@test "go.mod require block is read" {
    write go.mod $'module example.com/app\n\nrequire (\n\tgithub.com/spf13/cobra v1.9.1\n\thop.top/kit v0.5.0 // indirect\n)'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line go go.mod v0.5.0 hop-top-kit-go kit)" ]
}

@test "go.mod without kit is absent, not missing" {
    write go.mod $'module example.com/app\n\nrequire github.com/spf13/cobra v1.9.1'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line go go.mod absent hop-top-kit-go kit)" ]
}

@test "go.mod of kit itself and a replace line are not a dependency" {
    write go.mod $'module hop.top/kit\n\nreplace hop.top/kit => ../kit'
    detect "$DIR"
    [ "$output" = "$(line go go.mod absent hop-top-kit-go kit)" ]
}

@test "package.json with kit reports the range, dev dependencies too" {
    write package.json '{"name":"app","devDependencies":{"@hop-top/kit":"^0.5.0"}}'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line ts package.json ^0.5.0 hop-top-kit-ts kit-ts)" ]
}

@test "package.json without kit, and a similarly named package, is absent" {
    write package.json $'{\n  "name": "@hop-top/kit",\n  "dependencies": {\n    "@hop-top/kit-extra": "1.0.0"\n  }\n}'
    detect "$DIR"
    [ "$output" = "$(line ts package.json absent hop-top-kit-ts kit-ts)" ]
}

@test "pyproject.toml PEP 621 dependency" {
    write pyproject.toml $'[project]\nname = "app"\ndependencies = [\n  "hop-top-kit>=0.5.0",\n]'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line py pyproject.toml '>=0.5.0' hop-top-kit-py kit-py)" ]
}

@test "pyproject.toml table style, extras and normalised name" {
    write pyproject.toml $'[tool.poetry.dependencies]\nHop_Top.Kit = "^0.5"'
    detect "$DIR"
    [ "$output" = "$(line py pyproject.toml ^0.5 hop-top-kit-py kit-py)" ]
    write pyproject.toml $'[project]\ndependencies = ["hop-top-kit[serve] >= 0.5, < 1"]'
    detect "$DIR"
    [ "$output" = "$(line py pyproject.toml '>=0.5,<1' hop-top-kit-py kit-py)" ]
}

@test "pyproject.toml without kit, own project name, similar name: absent" {
    write pyproject.toml $'[project]\nname = "hop-top-kit"\ndependencies = ["hop-top-kit-extra>=1", "requests"]'
    detect "$DIR"
    [ "$output" = "$(line py pyproject.toml absent hop-top-kit-py kit-py)" ]
}

@test "requirements files: every requirements*.txt is a finding" {
    write requirements.txt $'# pinned\nrequests==2.0\nhop-top-kit==0.5.0'
    write requirements-dev.txt 'pytest'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "${lines[0]}" = "$(line py requirements-dev.txt absent hop-top-kit-py kit-py)" ]
    [ "${lines[1]}" = "$(line py requirements.txt ==0.5.0 hop-top-kit-py kit-py)" ]
    [ "${#lines[@]}" -eq 2 ]
}

@test "requirements.txt with an unpinned kit" {
    write requirements.txt $'hop_top_kit'
    detect "$DIR"
    [ "$output" = "$(line py requirements.txt unpinned hop-top-kit-py kit-py)" ]
}

@test "uv.lock reports the locked version" {
    write uv.lock $'version = 1\n\n[[package]]\nname = "requests"\nversion = "2.0.0"\n\n[[package]]\nname = "hop-top-kit"\nversion = "0.5.0"'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line py uv.lock 0.5.0 hop-top-kit-py kit-py)" ]
}

@test "uv.lock without kit is absent" {
    write uv.lock $'[[package]]\nname = "requests"\nversion = "2.0.0"\n\n[package.metadata]\nrequires-dist = [{ name = "hop-top-kit-extra" }]'
    detect "$DIR"
    [ "$output" = "$(line py uv.lock absent hop-top-kit-py kit-py)" ]
}

@test "Cargo.toml inline version, inline table and dependency table" {
    write Cargo.toml $'[package]\nname = "app"\n\n[dependencies]\nhop-top-kit = "0.5.0-beta.16"'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line rs Cargo.toml 0.5.0-beta.16 hop-top-kit-rs kit-rs)" ]
    write Cargo.toml $'[dependencies]\nhop-top-kit = { version = "0.5", features = ["serve"] }'
    detect "$DIR"
    [ "$output" = "$(line rs Cargo.toml 0.5 hop-top-kit-rs kit-rs)" ]
    write Cargo.toml $'[dependencies.hop-top-kit]\nversion = "0.6"\nfeatures = ["serve"]'
    detect "$DIR"
    [ "$output" = "$(line rs Cargo.toml 0.6 hop-top-kit-rs kit-rs)" ]
}

@test "Cargo.toml without kit is absent" {
    write Cargo.toml $'[package]\nname = "hop-top-kit"\n\n[dependencies]\nserde = "1"\nhop-top-kit-macros = "0.1"'
    detect "$DIR"
    [ "$output" = "$(line rs Cargo.toml absent hop-top-kit-rs kit-rs)" ]
}

@test "composer.json with and without kit" {
    write composer.json '{"require":{"php":">=8.4","hop-top/kit":"^0.5"}}'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line php composer.json ^0.5 hop-top-kit-php kit-php)" ]
    write composer.json '{"require":{"hop-top/kit-extra":"^1"}}'
    detect "$DIR"
    [ "$output" = "$(line php composer.json absent hop-top-kit-php kit-php)" ]
}

@test "polyglot tree: one line per manifest, fixed order, mixed kit status" {
    write composer.json '{"require":{"hop-top/kit":"^0.5"}}'
    write Cargo.toml $'[dependencies]\nserde = "1"'
    write package.json '{"dependencies":{"@hop-top/kit":"0.5.0"}}'
    write go.mod $'module example.com/app\n\nrequire hop.top/kit v0.5.0'
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 4 ]
    [ "${lines[0]}" = "$(line go go.mod v0.5.0 hop-top-kit-go kit)" ]
    [ "${lines[1]}" = "$(line ts package.json 0.5.0 hop-top-kit-ts kit-ts)" ]
    [ "${lines[2]}" = "$(line rs Cargo.toml absent hop-top-kit-rs kit-rs)" ]
    [ "${lines[3]}" = "$(line php composer.json ^0.5 hop-top-kit-php kit-php)" ]
}

@test "empty tree: no findings on stdout, a note on stderr, exit 0" {
    run --separate-stderr sh "$SCRIPT" "$DIR"
    [ "$status" -eq 0 ]
    [ -z "$output" ]
    [[ "$stderr" == *"no kit-relevant manifest"* ]]
}

@test "manifests below the directory are not read" {
    write sub/go.mod 'require hop.top/kit v0.5.0'
    run --separate-stderr sh "$SCRIPT" "$DIR"
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "default directory is the current one" {
    write go.mod 'require hop.top/kit v0.5.0'
    cd "$DIR"
    detect
    [ "$status" -eq 0 ]
    [ "$output" = "$(line go go.mod v0.5.0 hop-top-kit-go kit)" ]
}

@test "usage errors exit 2: extra argument, option, missing directory" {
    detect "$DIR" "$DIR"
    [ "$status" -eq 2 ]
    detect --bogus
    [ "$status" -eq 2 ]
    detect "$DIR/does-not-exist"
    [ "$status" -eq 2 ]
    write plain-file x
    detect "$DIR/plain-file"
    [ "$status" -eq 2 ]
}

@test "--help prints usage and exits 0" {
    detect --help
    [ "$status" -eq 0 ]
    [[ "$output" == usage:* ]]
}

@test "reads only: the tree is byte-identical after a run" {
    write go.mod 'require hop.top/kit v0.5.0'
    write package.json '{"dependencies":{"@hop-top/kit":"0.5.0"}}'
    before="$(cd "$DIR" && find . -exec ls -ld {} \; | sort; cat go.mod package.json)"
    detect "$DIR"
    after="$(cd "$DIR" && find . -exec ls -ld {} \; | sort; cat go.mod package.json)"
    [ "$before" = "$after" ]
}

@test "an unreadable manifest is reported, not skipped" {
    [ "$(id -u)" -ne 0 ] || skip "root reads everything"
    write go.mod 'require hop.top/kit v0.5.0'
    chmod 000 "$DIR/go.mod"
    detect "$DIR"
    [ "$status" -eq 0 ]
    [ "$output" = "$(line go go.mod unreadable hop-top-kit-go kit)" ]
}

@test "the script is executable and starts with the portable shebang" {
    [ -x "$SCRIPT" ]
    [ "$(head -n 1 "$SCRIPT")" = "#!/bin/sh" ]
}

@test "runs under dash (POSIX only)" {
    command -v dash >/dev/null 2>&1 || skip "dash not installed"
    write go.mod 'require hop.top/kit v0.5.0'
    write pyproject.toml $'[project]\ndependencies = ["hop-top-kit>=0.5.0"]'
    run dash "$SCRIPT" "$DIR"
    [ "$status" -eq 0 ]
    [ "${#lines[@]}" -eq 2 ]
}

@test "passes shellcheck as sh" {
    command -v shellcheck >/dev/null 2>&1 || skip "shellcheck not installed"
    run shellcheck --shell=sh "$SCRIPT"
    [ "$status" -eq 0 ]
}
