"""Runs the shared upgrade corpus in ``contracts/upgrade-v1``.

The vectors are the source of truth: a failure means this port drifted
from the contract, not that a vector should be edited to fit. Strict:
unknown fields fail, ``version`` must be ``v1`` and every JSON file in
the corpus must be consumed here or listed in ``PENDING``.
"""

from __future__ import annotations

import copy
import json
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit.upgrade import (
    DocApplied,
    DocGapError,
    DocInvalidError,
    DocNewerError,
    DocOptions,
    DocStep,
    DocStepError,
    DocumentError,
    DocVerifyError,
    DocVersionError,
    Migration,
    Registry,
    VerifyError,
    compare_versions,
    migrate_document,
    migrate_document_map,
)

#: Corpus files this runner consumes.
CONSUMED = {"versions.json", "document.json"}

#: Corpus files this runner lists but does not consume yet, with the
#: reason. A file here is reported as pending in the test run, never
#: silently ignored. Removing an entry means consuming the file.
PENDING = {
    "migrator.json": "Migrator not ported to this runner yet",
}

HEADER = {"$schema", "$id", "version", "description"}


def _corpus() -> Path:
    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "upgrade-v1"
        if target.is_dir():
            return target
    raise FileNotFoundError(f"contracts/upgrade-v1: not found walking up from {here}")


def _strict(obj: dict[str, Any], allowed: set[str], where: str) -> None:
    unknown = set(obj) - allowed
    assert not unknown, f"{where}: unknown fields {sorted(unknown)}"


def _load(name: str) -> dict[str, Any]:
    corpus = _corpus()
    for p in corpus.glob("*.json"):
        assert p.name in CONSUMED or p.name in PENDING, f"{p.name} has no runner"
    data = json.loads((corpus / name).read_text())
    assert data["version"] == "v1", f"{name}: version {data['version']}"
    return data


def _sign(n: int) -> int:
    return (n > 0) - (n < 0)


# -- corpus bookkeeping ------------------------------------------------------


def test_every_corpus_file_is_accounted_for() -> None:
    names = {p.name for p in _corpus().glob("*.json")}
    assert names == CONSUMED | set(PENDING)
    assert not CONSUMED & set(PENDING)


@pytest.mark.parametrize("name", sorted(PENDING))
def test_pending_file(name: str) -> None:
    data = json.loads((_corpus() / name).read_text())
    assert data["version"] == "v1"
    pytest.skip(f"{name} pending: {PENDING[name]}")


# -- versions.json -----------------------------------------------------------

_VERSIONS = _load("versions.json")
_strict(_VERSIONS, HEADER | {"compare", "pending"}, "versions.json")
_COMPARE = _VERSIONS["compare"]
_PENDING = _VERSIONS["pending"]


@pytest.mark.parametrize("case", _COMPARE, ids=[c["name"] for c in _COMPARE])
def test_compare(case: dict[str, Any]) -> None:
    _strict(case, {"name", "a", "b", "cmp"}, case["name"])
    assert _sign(compare_versions(case["a"], case["b"])) == case["cmp"]
    assert _sign(compare_versions(case["b"], case["a"])) == -case["cmp"]


@pytest.mark.parametrize("case", _PENDING, ids=[c["name"] for c in _PENDING])
def test_pending(case: dict[str, Any]) -> None:
    _strict(case, {"name", "migrations", "schema", "from", "to", "pending"}, case["name"])
    reg = Registry()
    for m in case["migrations"]:
        _strict(m, {"schema", "version"}, case["name"])
        reg.register(Migration(version=m["version"], schema=m["schema"], up=lambda: None))
    got = [m.version for m in reg.pending(case["schema"], case["from"], case["to"])]
    assert got == case["pending"]


def test_corpus_sections_not_empty() -> None:
    assert _COMPARE
    assert _PENDING


# -- document.json -----------------------------------------------------------

_DOCUMENT = _load("document.json")
_strict(_DOCUMENT, HEADER | {"scenarios"}, "document.json")
_SCENARIOS = _DOCUMENT["scenarios"]

_KINDS: dict[str, type[Exception]] = {
    "newer": DocNewerError,
    "version": DocVersionError,
    "gap": DocGapError,
    "step": DocStepError,
    "verify": VerifyError,
    "invalid": DocInvalidError,
}


class _BoomError(Exception):
    pass


def _lookup(doc: dict[str, Any], path: str) -> tuple[Any, bool]:
    cur: Any = doc
    for seg in path.split("."):
        if not isinstance(cur, dict) or seg not in cur:
            return None, False
        cur = cur[seg]
    return cur, True


def _set(doc: dict[str, Any], path: str, value: Any) -> None:
    segs = path.split(".")
    cur = doc
    for seg in segs[:-1]:
        nxt = cur.get(seg)
        if not isinstance(nxt, dict):
            nxt = {}
            cur[seg] = nxt
        cur = nxt
    cur[segs[-1]] = value


def _steps(cases: list[dict[str, Any]]) -> list[DocStep]:
    out: list[DocStep] = []
    for c in cases:
        _strict(c, {"from", "to", "note", "ops", "verify"}, "step")
        for op in c.get("ops", []):
            _strict(op, {"set", "rename", "fail"}, "op")
            assert len(op) == 1, f"op must hold one of set, rename, fail: {sorted(op)}"
            if "set" in op:
                _strict(op["set"], {"path", "value"}, "op.set")
            if "rename" in op:
                _strict(op["rename"], {"from", "to"}, "op.rename")
        if "verify" in c:
            _strict(c["verify"], {"require"}, "step.verify")

        def apply(doc: dict[str, Any], c: dict[str, Any] = c) -> dict[str, Any]:
            # In place, on purpose: the migrator must hand steps a copy.
            for op in c.get("ops", []):
                if "set" in op:
                    _set(doc, op["set"]["path"], copy.deepcopy(op["set"]["value"]))
                elif "rename" in op:
                    r = op["rename"]
                    if r["from"] in doc:
                        doc[r["to"]] = doc.pop(r["from"])
                elif "fail" in op:
                    raise _BoomError(op["fail"])
            return doc

        verify = None
        if "verify" in c:
            req = c["verify"]["require"]

            def verify(doc: dict[str, Any], req: str = req) -> None:
                if not _lookup(doc, req)[1]:
                    raise _BoomError("missing " + req)

        out.append(
            DocStep(
                from_version=c["from"],
                to_version=c["to"],
                apply=apply,
                verify=verify,
                note=c.get("note", ""),
            )
        )
    return out


def _report(r: Any) -> dict[str, Any]:
    return {
        "from": r.from_version,
        "to": r.to_version,
        "applied": [
            {"from": a.from_version, "to": a.to_version, "note": a.note} for a in r.applied
        ],
    }


@pytest.mark.parametrize("sc", _SCENARIOS, ids=[s["name"] for s in _SCENARIOS])
def test_document(sc: dict[str, Any]) -> None:
    _strict(sc, {"name", "doc", "text", "options", "steps", "expect"}, sc["name"])
    opts_in = sc.get("options", {})
    _strict(opts_in, {"version_field", "latest", "missing_is_zero"}, sc["name"])
    expect = sc["expect"]
    _strict(expect, {"doc", "text", "report", "error", "kind"}, sc["name"])
    opts = DocOptions(
        version_field=opts_in.get("version_field", ""),
        latest=opts_in.get("latest", 0),
        missing_is_zero=opts_in.get("missing_is_zero", False),
    )
    steps = _steps(sc.get("steps", []))
    raw = sc["text"].encode() if "text" in sc else json.dumps(sc["doc"]).encode()

    def check(via: str, got: Any, report: Any, err: Exception | None) -> None:
        if "error" in expect:
            assert err is not None, f"{via}: no error, want {expect['error']!r}"
            assert str(err) == expect["error"], via
            assert isinstance(err, _KINDS[expect["kind"]]), f"{via}: {err!r} not {expect['kind']}"
            assert isinstance(err, DocumentError), via
            return
        assert err is None, f"{via}: unexpected {err!r}"
        if "doc" in expect:
            assert got == expect["doc"], via
        if "report" in expect:
            assert _report(report) == expect["report"], via

    before = bytes(raw)
    out = report = err = None
    try:
        out, report = migrate_document(raw, steps, opts)
    except DocumentError as e:
        err = e
    assert raw == before, "input bytes changed"
    parsed = json.loads(out) if err is None else None
    check("bytes", parsed, report, err)
    if "text" in expect and err is None:
        assert out.decode() == expect["text"], "bytes: want byte-identical"

    # The map form, when the input is an object: same result, and the
    # caller's map is untouched however the steps mutate. Exact literals
    # (text) only survive the bytes form.
    if "text" not in sc and isinstance(sc["doc"], dict):
        doc = copy.deepcopy(sc["doc"])
        snapshot = copy.deepcopy(doc)
        got = report = err = None
        try:
            got, report = migrate_document_map(doc, steps, opts)
        except DocumentError as e:
            err = e
        assert doc == snapshot, "map: caller's document changed"
        check("map", got, report, err)


def test_document_surface_is_exported() -> None:
    assert issubclass(DocVerifyError, DocumentError)
    assert issubclass(DocVerifyError, VerifyError)
    assert DocApplied(from_version=1, to_version=2, note="n").note == "n"
