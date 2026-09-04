#!/usr/bin/env python3
"""Compare one runtime's ordering observations against the expected contract.

Reads a runner's JSONL observation file and ``expected/ordering.json``, then
reports PASS / FAIL / GAP per case/format.

WHY THIS IS NOT THE TELEMETRY COMPARISON PATH
---------------------------------------------
``run.sh``'s ``normalise_jsonl`` canonicalises envelopes with
``json.dump(..., sort_keys=True)``. That is correct for the telemetry contract,
where key order carries no meaning — and catastrophic here, because key order
is the ENTIRE subject of these fixtures. Sorting would make every runtime look
identical and the suite would assert nothing.

So this comparator never sorts an observed sequence. ``sequence`` is compared
element-by-element as an ordered list. Membership comparison and
order-insensitive deep-equality are both deliberately absent: rs's old
``Value``-to-``Value`` test compared unordered maps and was therefore inert
against the very bug it appeared to cover, and ``toEqual`` on JS objects has
the same hazard. Neither mistake is repeated here.

The runners themselves obtain ``sequence`` by RE-PARSING their own rendered
bytes with an order-preserving reader, so the observation reflects what was
actually serialized rather than what was passed in.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def load_observations(path: Path) -> dict[tuple[str, str], dict]:
    obs: dict[tuple[str, str], dict] = {}
    for line in path.read_text().splitlines():
        if not line.strip():
            continue
        rec = json.loads(line)
        obs[(rec["case"], rec["format"])] = rec
    return obs


def gap_applies(gaps: dict, lang: str, case: str, fmt: str) -> str | None:
    """Return a gap key when this runtime/case/format is a recorded gap."""
    for key, g in gaps.items():
        if key.startswith("_"):
            continue
        runtimes = g.get("runtime")
        runtimes = [runtimes] if isinstance(runtimes, str) else (runtimes or [])
        if lang not in runtimes:
            continue
        cases = g.get("cases")
        formats = g.get("formats")
        if cases is not None and case not in cases:
            continue
        if formats is not None and fmt not in formats:
            continue
        if cases is None and formats is None:
            continue
        return key
    return None


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print("usage: compare_order.py <lang> <observations.jsonl> <expected.json>")
        return 2
    lang, obs_path, exp_path = argv[1], Path(argv[2]), Path(argv[3])

    obs = load_observations(obs_path)
    expected = json.loads(exp_path.read_text())
    cases = expected["cases"]
    gaps = expected["known_gaps"]
    hk = expected["header_key_enforced"]

    failures: list[str] = []
    surfaced: list[str] = []
    passed = 0
    skipped = 0

    for case_name, per_format in cases.items():
        for fmt, want in per_format.items():
            rec = obs.get((case_name, fmt))
            if rec is None:
                # A runtime that never reported this case/format at all.
                # Only legitimate when the fixture excludes it (e.g. Go is
                # opted out of a case); otherwise it is a real failure.
                continue
            if rec.get("status") == "unsupported":
                gap = gap_applies(gaps, lang, case_name, fmt)
                surfaced.append(
                    f"  {case_name}/{fmt}: formatter not implemented"
                    + (f" [known gap: {gap}]" if gap else " [UNRECORDED]")
                )
                skipped += 1
                continue
            if rec.get("status") != "ok":
                failures.append(f"  {case_name}/{fmt}: runner status={rec.get('status')}")
                continue

            got_seq = rec.get("sequence", [])
            want_seq = want["sequence"]
            want_empty = want.get("empty", False)
            got_empty = rec.get("empty", False)

            # ORDERED comparison. Never sorted, never treated as a set.
            seq_ok = got_seq == want_seq
            empty_ok = got_empty == want_empty

            if seq_ok and empty_ok:
                passed += 1
                continue

            gap = gap_applies(gaps, lang, case_name, fmt)
            detail = (
                f"  {case_name}/{fmt}: expected sequence {want_seq} got {got_seq}"
                if not seq_ok
                else f"  {case_name}/{fmt}: expected empty={want_empty} got empty={got_empty}"
            )
            if gap:
                surfaced.append(f"{detail} [known gap: {gap}]")
            else:
                failures.append(detail)

    # Contract rule 3.
    hk_rec = obs.get(("header-key-enforced", "-"))
    if hk_rec is not None:
        if lang in hk.get("not_applicable", []):
            if hk_rec.get("status") == "n/a":
                surfaced.append(
                    '  header-key-enforced: n/a — a table:"" tag cannot express '
                    "a header/key split, which is why the rule exists"
                )
            else:
                failures.append(f"  header-key-enforced: expected n/a for {lang}, got {hk_rec}")
        elif lang in hk.get("applies_to", []):
            if hk_rec.get("rejected") is hk["rejected"]:
                passed += 1
            else:
                failures.append(
                    f"  header-key-enforced: expected rejected={hk['rejected']} "
                    f"got rejected={hk_rec.get('rejected')}"
                )

    print(f"    checks passed: {passed}  failed: {len(failures)}  surfaced: {len(surfaced)}")
    if surfaced:
        print("    known parity gaps (surfaced, not failures):")
        for s in surfaced:
            print(f"    {s}")
    if failures:
        print("    FAILURES:")
        for f in failures:
            print(f"    {f}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
