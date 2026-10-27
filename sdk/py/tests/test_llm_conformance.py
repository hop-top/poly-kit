"""llm-v1 conformance runner for the Python SDK.

Loads ``contracts/llm-v1/vectors.json`` from the repo root. The Python
SDK implements the provider-neutral ``tool_names`` section; every other
section pins an adapter or core surface this SDK does not have, and is
listed in :data:`NOT_PORTED` with the reason. Each of those sections
reports as an explicit skip carrying that reason, and a section the
runner neither consumes nor lists fails the run.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit import llm
from hop_top_kit.llm_toolnames import (
    ANTHROPIC_TOOL_NAMES,
    GEMINI_TOOL_NAMES,
    OPENAI_TOOL_NAMES,
    ToolNameMode,
    ToolNames,
)

RULES = {
    "anthropic": ANTHROPIC_TOOL_NAMES,
    "openai": OPENAI_TOOL_NAMES,
    "gemini": GEMINI_TOOL_NAMES,
}

CONSUMED = {"tool_names"}

_NO_ANTHROPIC = "hop_top_kit has no anthropic adapter; this section pins the anthropic adapter"

# Sections this runner does not run, and why. Keep the reasons precise:
# they are what a reader sees in the skip summary.
NOT_PORTED: dict[str, str] = {
    "redact_uri": "hop_top_kit.llm has no RedactURI counterpart",
    "parse_uri": "hop_top_kit.llm.parse_uri is not yet checked against these vectors",
    "anthropic_request": _NO_ANTHROPIC,
    "anthropic_request_error": _NO_ANTHROPIC,
    "anthropic_headers": _NO_ANTHROPIC,
    "anthropic_stream": _NO_ANTHROPIC,
    "anthropic_accumulate": _NO_ANTHROPIC,
    "anthropic_error": _NO_ANTHROPIC,
    "anthropic_limits": _NO_ANTHROPIC,
    "anthropic_response": _NO_ANTHROPIC,
    "anthropic_tool_names": _NO_ANTHROPIC,
    "credential_headers": _NO_ANTHROPIC,
    "key_endpoint": "hop_top_kit.llm has no key-source or endpoint policy",
    "usage_cost": "hop_top_kit.llm.Usage carries no pricing",
}

_META = {"version", "description"}
_TOOL_NAMES_FIELDS = {"name", "rule", "mode", "tools", "want", "error", "calls", "want_calls"}
_ERROR_FIELDS = {"reason", "name", "other"}


def _locate() -> Path:
    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "llm-v1" / "vectors.json"
        if target.is_file():
            return target
    raise FileNotFoundError(f"contracts/llm-v1/vectors.json: not found walking up from {here}")


_VECTORS: dict[str, Any] = json.loads(_locate().read_text(encoding="utf-8"))


def test_version_is_v1() -> None:
    assert _VECTORS["version"] == "v1"


def test_every_section_is_consumed_or_listed() -> None:
    sections = set(_VECTORS) - _META
    unknown = sections - CONSUMED - set(NOT_PORTED)
    assert not unknown, f"sections neither run nor listed in NOT_PORTED: {sorted(unknown)}"
    stale = set(NOT_PORTED) - sections
    assert not stale, f"NOT_PORTED lists sections the file no longer has: {sorted(stale)}"


@pytest.mark.parametrize("section", sorted(NOT_PORTED))
def test_not_ported(section: str) -> None:
    assert _VECTORS[section], f"{section} is empty"
    pytest.skip(f"{section} ({len(_VECTORS[section])} vectors): {NOT_PORTED[section]}")


def _tool_names_vectors() -> list[Any]:
    vs = _VECTORS["tool_names"]
    assert vs, "tool_names is empty"
    return [pytest.param(v, id=v["name"]) for v in vs]


@pytest.mark.parametrize("v", _tool_names_vectors())
def test_tool_names(v: dict[str, Any]) -> None:
    extra = set(v) - _TOOL_NAMES_FIELDS
    assert not extra, f"unknown fields {sorted(extra)}"
    assert v["rule"] in RULES, f"unknown rule {v['rule']!r}"
    rule = RULES[v["rule"]]
    mode = ToolNameMode(v.get("mode") or "sanitize")
    defs = [llm.ToolDef(name=n, description="") for n in v["tools"]]

    if "error" in v:
        assert "want" not in v
        assert not set(v["error"]) - _ERROR_FIELDS
        with pytest.raises(llm.ToolNameError) as ei:
            ToolNames.build(rule, mode, defs)
        err = ei.value
        assert err.reason == v["error"]["reason"]
        assert err.name == v["error"]["name"]
        assert err.other == v["error"]["other"]
        assert err.provider == rule.provider
        assert not llm.is_fallbackable(err)
        return

    names = ToolNames.build(rule, mode, defs)
    got = [names.wire(n) for n in v["tools"]]
    assert got == v["want"]
    for w in got:
        assert rule.valid(w), f"wire name {w!r} invalid for {v['rule']}"
    if mode is ToolNameMode.SANITIZE:
        assert [rule.sanitize(n) for n in v["tools"]] == v["want"]
    back = [names.original(c) for c in v.get("calls", [])]
    assert back == v.get("want_calls", [])
