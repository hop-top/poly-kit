#!/usr/bin/env python3
"""Python runner for the cross-language telemetry contract harness.

Reads ``fixtures/input.json``, instantiates ``hop_top_kit.telemetry.Client``
with the jsonl sink, calls ``record()``, then ``shutdown()`` to drain. The
orchestrator parses the resulting JSONL file and diffs it against
``expected/envelope.json``.

Pre-conditions (set by the orchestrator, NOT this runner):
  - ``XDG_STATE_HOME`` and ``XDG_CONFIG_HOME`` point into the temp dir.
  - The 32-byte install_id fixture is pre-seeded at
    ``$XDG_STATE_HOME/kit/telemetry/installation_id``.
  - The consent fixture is pre-seeded at ``$XDG_CONFIG_HOME/kit/telemetry.yaml``.
  - ``KIT_TELEMETRY_MODE=full``.
  - ``KIT_TELEMETRY_SINK=jsonl``.
  - ``KIT_TELEMETRY_SINK_FILE`` is set to a writable temp path.
  - ``HOME=/test/home`` (so the $HOME redactor rewrites our fixture path).
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    # __file__ → .../sdk/tests/cross-lang/runners/py/record.py
    # parents[4] is sdk/. The Python SDK source lives at sdk/py/.
    sdk_py = Path(__file__).resolve().parents[4] / "py"
    # Make `import hop_top_kit` resolve from the in-tree SDK (no install needed).
    sys.path.insert(0, str(sdk_py))

    from hop_top_kit.telemetry import Client  # noqa: E402

    # The orchestrator may template a per-run input.json (with HARNESS_HOME
    # baked into home_path); fall back to the static fixture otherwise.
    override = os.environ.get("KIT_CROSS_LANG_INPUT")
    if override:
        payload = json.loads(Path(override).read_text())
    else:
        fixtures = Path(__file__).resolve().parents[2] / "fixtures"
        payload = json.loads((fixtures / "input.json").read_text())

    client = Client(
        sdk_version="cross-lang-test",
    )
    client.record(payload["event"], payload["attrs"])
    client.shutdown(timeout=5.0)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
