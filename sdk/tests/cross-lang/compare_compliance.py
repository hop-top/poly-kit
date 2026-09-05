#!/usr/bin/env python3
"""Compare one port's compliance observation against the expected contract.

Reads a runner's JSON observation and ``expected/compliance.json``, then
reports the per-field verdicts.

WHAT IS PINNED, AND WHY IT IS NOT BYTES
---------------------------------------
Report *bytes* are not comparable across ports: Go marshals a struct, TS
serialises an interface, Python a dataclass, and each carries its own
field elision rules for empty `details` / `suggestion`. None of that is
contractual.

What IS contractual is the verdict: the score, the denominator, and each
factor's status. So the comparator checks exactly those three and nothing
else. `factors` is compared as a MAPPING keyed by factor number rather
than as a list, because result order is not part of this contract — the
sibling ordering harness is where sequence is the subject.

The denominator is checked separately from the factor map and reported on
its own line. A port that ports F13's check but forgets the 12/13
denominator bump passes every factor and still fails here, which is the
half of the parity that a factor-only comparison would miss.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print("usage: compare_compliance.py <lang> <observation.json> <expected.json>")
        return 2
    lang, obs_path, exp_path = argv[1], Path(argv[2]), Path(argv[3])

    obs = json.loads(obs_path.read_text())
    expected = json.loads(exp_path.read_text())

    failures: list[str] = []
    passed = 0

    # Denominator first: it is the field most likely to be wrong on a port
    # that copied the check but not the score math.
    if obs.get("total") == expected["total"]:
        passed += 1
    else:
        failures.append(
            f"  total: expected {expected['total']} got {obs.get('total')}"
            " — the opt-in fixture must widen the denominator to 13"
        )

    if obs.get("score") == expected["score"]:
        passed += 1
    else:
        failures.append(f"  score: expected {expected['score']} got {obs.get('score')}")

    want_factors = expected["factors"]
    got_factors = obs.get("factors", {})
    for key in sorted(want_factors, key=int):
        want = want_factors[key]
        got = got_factors.get(key)
        if got is None:
            failures.append(
                f"  F{key} ({expected['names'][key]}): missing from the report entirely"
            )
            continue
        if got != want:
            failures.append(
                f"  F{key} ({expected['names'][key]}): expected {want} got {got}"
            )
            continue
        passed += 1

    for key in sorted(got_factors, key=int):
        if key not in want_factors:
            failures.append(f"  F{key}: reported but not in the contract")

    # Factor names are part of the parity claim: the report renders them,
    # so a port that spells one differently produces a different report.
    want_names = expected["names"]
    got_names = obs.get("names", {})
    for key in sorted(want_names, key=int):
        got = got_names.get(key)
        if got is not None and got != want_names[key]:
            failures.append(
                f"  F{key} name: expected {want_names[key]!r} got {got!r}"
            )

    print(f"    checks passed: {passed}  failed: {len(failures)}")
    if failures:
        print("    FAILURES:")
        for f in failures:
            print(f"    {f}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
