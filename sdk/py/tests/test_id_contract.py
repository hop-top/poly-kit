"""Cross-language parity contract test (tlc T-0753).

Loads ``contracts/typeid-v1/fixtures.json`` from the repo root and
asserts that this Python SDK's encode (via ``typeid-python``'s
``TypeID.from_uuid``) and decode (via the kit's :func:`parse`) agree
with the canonical wire form shared by go/rs/ts/php. A divergence here
means either ``typeid-python`` drifted or the contract was edited
without updating all five SDKs.
"""

from __future__ import annotations

import json
from pathlib import Path
from uuid import UUID

import pytest
from typeid import TypeID

from hop_top_kit.id import parse

# ---------------------------------------------------------------------------
# Contract loader
# ---------------------------------------------------------------------------


def _locate_contract() -> Path:
    """Walk up from this test file until we find the fixtures.json.

    Going through ``__file__`` (not CWD) means the loader works under
    both ``pytest`` (CWD = sdk/py) and editor / IDE invocations whose
    CWD may vary.
    """

    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "typeid-v1" / "fixtures.json"
        if target.is_file():
            return target
    raise FileNotFoundError(f"contracts/typeid-v1/fixtures.json: not found walking up from {here}")


def _load() -> dict:
    return json.loads(_locate_contract().read_text(encoding="utf-8"))


_CONTRACT = _load()
_VECTORS = _CONTRACT["vectors"]


def _vector_id(v: dict) -> str:
    return v["name"]


def _skipped_in_py(v: dict) -> bool:
    return "py" in v.get("skip_in", [])


# ---------------------------------------------------------------------------
# Metadata
# ---------------------------------------------------------------------------


def test_contract_metadata() -> None:
    assert _CONTRACT["version"] == "v1", "contract version drift"
    assert _CONTRACT["spec"] == "jetify-typeid-v0.3", "contract spec drift"
    assert _VECTORS, "contract has no vectors"


# ---------------------------------------------------------------------------
# Generation: upstream encoder must produce the pinned canonical string.
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _VECTORS, ids=_vector_id)
def test_contract_generation(v: dict) -> None:
    if _skipped_in_py(v):
        pytest.skip(f"skip_in includes 'py': {v.get('note', '')}")

    # ``typeid-python`` rejects an empty ``prefix=`` kwarg but accepts
    # ``prefix=None`` for the bare-suffix form. Coerce empty -> None so
    # the contract's empty-prefix vector (if applicable to py) flows
    # through the same code path the parser uses on the way back.
    prefix_arg = v["prefix"] or None
    tid = TypeID.from_uuid(suffix=UUID(v["uuid"]), prefix=prefix_arg)
    got = str(tid)
    assert got == v["typeid"], (
        f"canonical typeid drift on vector {v['name']!r} "
        f"(prefix={v['prefix']!r} uuid={v['uuid']}):\n"
        f"  got:  {got!r}\n  want: {v['typeid']!r}"
    )


# ---------------------------------------------------------------------------
# Parse: kit-level parse() must recover (prefix, uuid).
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("v", _VECTORS, ids=_vector_id)
def test_contract_parse(v: dict) -> None:
    if _skipped_in_py(v):
        pytest.skip(f"skip_in includes 'py': {v.get('note', '')}")

    parsed = parse(v["typeid"])
    assert parsed.prefix == v["prefix"], (
        f"prefix mismatch on vector {v['name']!r}: want {v['prefix']!r}, got {parsed.prefix!r}"
    )
    assert parsed.uuid == UUID(v["uuid"]), (
        f"uuid mismatch on vector {v['name']!r}: want {v['uuid']}, got {parsed.uuid}"
    )
