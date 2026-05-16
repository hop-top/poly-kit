#!/usr/bin/env bash
# test_helper.bash — shared assertions for bats tests

assert_file_exists() {
  [ -f "$1" ] || { echo "missing: $1"; return 1; }
}

assert_file_not_exists() {
  [ ! -f "$1" ] || { echo "unexpected: $1"; return 1; }
}

assert_dir_exists() {
  [ -d "$1" ] || { echo "missing dir: $1"; return 1; }
}

assert_file_contains() {
  [ -f "$1" ] || { echo "missing: $1"; return 1; }
  grep -qF "$2" "$1" || {
    echo "$1 missing: $2"; return 1
  }
}

assert_file_not_contains() {
  [ ! -f "$1" ] && return 0
  ! grep -qF "$2" "$1" || {
    echo "$1 should not contain: $2"; return 1
  }
}
