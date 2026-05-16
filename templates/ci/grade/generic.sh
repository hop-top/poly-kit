#!/usr/bin/env bash
# Adopter: adapt to your CI. Set KIT_CONFORMANCE_TOKEN +
# KIT_CONFORMANCE_SERVICE in env.
set -euo pipefail
: "${KIT_CONFORMANCE_TOKEN:?KIT_CONFORMANCE_TOKEN env required}"
: "${KIT_CONFORMANCE_SERVICE:?KIT_CONFORMANCE_SERVICE env required}"
CASSETTE_DIR="${CASSETTE_DIR:-./testdata/cassettes/conformance}"
kit conformance grade "$CASSETTE_DIR" --format=json
