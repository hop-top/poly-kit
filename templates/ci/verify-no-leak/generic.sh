#!/usr/bin/env bash
# Adopter: adapt to your CI. BASE_REF defaults to origin/main.
set -euo pipefail
BASE="${BASE_REF:-origin/main}"
kit conformance verify-no-leak --diff="${BASE}...HEAD"
kit conformance verify-no-leak --commit-range="${BASE}..HEAD"
