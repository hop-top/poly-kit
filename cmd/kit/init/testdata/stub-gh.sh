#!/bin/bash
# stub-gh: records args + returns canned output. Configurable via env:
#   STUB_GH_LOG=<file>   - append args to this file
#   STUB_GH_OUT=<text>   - print this text to stdout (default: "https://github.com/foo/bar")
#   STUB_GH_FAIL=1       - exit 1 instead of 0
set -e
if [ -n "${STUB_GH_LOG:-}" ]; then
  echo "$@" >> "$STUB_GH_LOG"
fi
if [ -n "${STUB_GH_FAIL:-}" ]; then
  exit 1
fi
echo "${STUB_GH_OUT:-https://github.com/foo/bar}"
