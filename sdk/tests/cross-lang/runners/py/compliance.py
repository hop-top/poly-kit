#!/usr/bin/env python3
"""Python runner for the cross-language compliance conformance harness.

Runs the in-tree SDK's compliance checker against the shared opt-in
fixture and emits the observed score, denominator, and per-factor status
as a single stable JSON object to ``KIT_CROSS_LANG_COMPLIANCE_OUT``.

Only the STATIC pass runs (empty binary path). Runtime checks execute a
binary, which no port could agree on across languages, and F13 is a
static check in every port anyway.

Keys are emitted sorted and ``factors`` is an object keyed by factor
number rather than a list, so a port that reorders its results without
changing any status still compares equal. Order is not the subject here
— the score, the denominator, and the per-factor verdicts are.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path


def main() -> int:
    fixture = os.environ.get("KIT_CROSS_LANG_COMPLIANCE_FIXTURE", "")
    out = os.environ.get("KIT_CROSS_LANG_COMPLIANCE_OUT", "")
    if not fixture or not out:
        print(
            "KIT_CROSS_LANG_COMPLIANCE_FIXTURE and _OUT must be set",
            file=sys.stderr,
        )
        return 2

    # Import the in-tree SDK rather than an installed copy so the harness
    # tests the working tree, which is the whole point of a parity gate.
    sdk_root = Path(__file__).resolve().parents[3] / "py"
    sys.path.insert(0, str(sdk_root))
    from hop_top_kit.compliance import run

    report = run("", fixture)

    obs = {
        "lang": "py",
        "score": report.score,
        "total": report.total,
        "factors": {str(int(r.factor)): r.status for r in report.results},
        "names": {str(int(r.factor)): r.name for r in report.results},
    }

    with open(out, "w") as fh:
        json.dump(obs, fh, sort_keys=True, indent=2)
        fh.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
