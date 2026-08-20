#!/usr/bin/env python3
"""Python runner for the cross-language column-ordering conformance harness.

Reads ``fixtures/ordering.json``, renders every case in every listed format
through the in-tree SDK, then RE-PARSES its own rendered bytes to observe the
column sequence the formatter actually serialized. Emits one JSON object per
case/format to the path in ``KIT_CROSS_LANG_ORDER_OUT``.

Re-parsing rather than reporting the input is the whole point: it is the only
way to observe serialized key ORDER, which is exactly what a canonicalizing
comparison (``sort_keys=True``) would destroy. Nothing here sorts keys.
"""

from __future__ import annotations

import csv
import io
import json
import os
import sys
from pathlib import Path


def _emit(records: list[dict], path: str) -> None:
    with open(path, "w") as fh:
        for rec in records:
            fh.write(json.dumps(rec, sort_keys=True) + "\n")


def _seq_from_table(text: str) -> tuple[list[str], bool]:
    """Column sequence from a table/text-ish header line."""
    lines = [ln for ln in text.splitlines() if ln.strip()]
    if not lines:
        return [], True
    return lines[0].split(), False


def _seq_from_csv(text: str) -> tuple[list[str], bool]:
    lines = [ln for ln in text.splitlines() if ln.strip()]
    if not lines:
        return [], True
    return next(csv.reader([lines[0]])), False


def _seq_from_text(text: str) -> tuple[list[str], bool]:
    """text formatter is kv style: one ``key=value`` per line, row-major."""
    keys: list[str] = []
    for ln in text.splitlines():
        if not ln.strip():
            break
        if "=" not in ln:
            continue
        keys.append(ln.split("=", 1)[0].strip())
    return keys, not keys


def _seq_from_json(text: str) -> tuple[list[str], bool]:
    if not text.strip():
        return [], True
    # json.loads yields insertion-ordered dicts; key order survives exactly.
    doc = json.loads(text)
    if isinstance(doc, list):
        if not doc:
            return [], True
        return list(doc[0].keys()), False
    return list(doc.keys()), False


def _seq_from_yaml(text: str) -> tuple[list[str], bool]:
    if not text.strip():
        return [], True
    import yaml

    doc = yaml.safe_load(text)
    if doc is None:
        return [], True
    if isinstance(doc, list):
        if not doc:
            return [], True
        first = doc[0]
        if not isinstance(first, dict):
            return [], True
        # PyYAML >= 5.1 preserves mapping insertion order on load.
        return list(first.keys()), False
    return list(doc.keys()), False


_EXTRACT = {
    "table": _seq_from_table,
    "json": _seq_from_json,
    "yaml": _seq_from_yaml,
    "csv": _seq_from_csv,
    "text": _seq_from_text,
}


def main() -> int:
    sdk_py = Path(__file__).resolve().parents[4] / "py"
    sys.path.insert(0, str(sdk_py))

    from hop_top_kit.output import ColumnSpec, default_registry
    from hop_top_kit.output.formatter import parse_options

    fixtures = Path(__file__).resolve().parents[2] / "fixtures"
    spec_doc = json.loads((fixtures / "ordering.json").read_text())
    out_path = os.environ["KIT_CROSS_LANG_ORDER_OUT"]

    portable = spec_doc["portable_formats"]
    extended = spec_doc["extended_formats"]
    records: list[dict] = []

    for case in spec_doc["cases"]:
        formats = portable if case["formats"] == "portable" else extended
        columns = [ColumnSpec(n, n) for n in case["spec"]] if case["spec"] is not None else None
        for fmt in formats:
            formatter = default_registry.lookup(fmt)
            if formatter is None:
                records.append(
                    {
                        "case": case["name"],
                        "format": fmt,
                        "status": "unsupported",
                    }
                )
                continue
            buf = io.StringIO()
            opts = parse_options([], formatter.options())
            formatter.render(buf, case["rows"], opts, case["cols"], columns)
            rendered = buf.getvalue()
            seq, empty = _EXTRACT[fmt](rendered)
            records.append(
                {
                    "case": case["name"],
                    "format": fmt,
                    "status": "ok",
                    "sequence": seq,
                    "empty": empty,
                }
            )

    # Contract rule 3: a header != key ColumnSpec must not round-trip.
    # py enforces in ColumnSpec.__post_init__.
    try:
        ColumnSpec("Name", "name")
        rejected = False
    except Exception:
        rejected = True
    records.append(
        {
            "case": "header-key-enforced",
            "format": "-",
            "status": "ok",
            "rejected": rejected,
        }
    )

    _emit(records, out_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
