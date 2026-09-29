"""The SDK downloads the kit release it pins: hold that pin to the release.

``VERSION`` must equal the root (``kit``) package's version in the
release-please manifest, and the release PR must bump it: the file is a
generic extra-file of the root package and the pin's line carries the
generic updater's marker.
"""

from __future__ import annotations

import json
import re
from pathlib import Path

from kit_engine import _binary

ROOT = Path(__file__).resolve().parents[4]
BINARY_PY = "engine/sdk/py-kit-engine/kit_engine/_binary.py"
MARKER = "x-release-please-version"


def _manifest_kit_version() -> str:
    manifest = json.loads((ROOT / ".github" / ".release-please-manifest.json").read_text())
    return manifest["."]


def test_pin_equals_released_kit_version():
    assert _manifest_kit_version() == _binary.VERSION


def test_pin_is_bumped_by_the_release_pr():
    config = json.loads((ROOT / ".github" / "release-please-config.json").read_text())
    extra = config["packages"]["."].get("extra-files", [])
    assert {"type": "generic", "path": BINARY_PY} in extra

    marked = [line for line in (ROOT / BINARY_PY).read_text().splitlines() if MARKER in line]
    assert len(marked) == 1
    assert re.match(r'^VERSION = "[^"]+"', marked[0])
