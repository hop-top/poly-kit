"""Runs the delegation + inference slice of ``contracts/cli-root-v1``.

The vectors are the source of truth: a failure means this port drifted
from the contract (Go is canonical), not that a vector should be edited.
Every vector must be either replayed or listed in ``SKIPS`` with a reason;
a new vector nobody classified fails the suite instead of vanishing.
"""

from __future__ import annotations

import inspect
import json
from pathlib import Path
from typing import Any

import pytest
import typer
from typer.testing import CliRunner

from hop_top_kit.cli import annotate, create_app, is_dry_run
from hop_top_kit.gate import PromptSource
from hop_top_kit.policy import default_policy_loader


def _vectors() -> dict[str, Any]:
    here = Path(__file__).resolve().parent
    for cand in (here, *here.parents):
        f = cand / "contracts" / "cli-root-v1" / "vectors.json"
        if f.is_file():
            return json.loads(f.read_text())
    raise FileNotFoundError("contracts/cli-root-v1/vectors.json not found")


DOC = _vectors()
TREES: dict[str, Any] = DOC["trees"]
CASES: list[dict[str, Any]] = DOC["cases"]

#: Trees whose behavior this slice does not port.
SKIP_TREES = {
    "main": "root surface (persistent flags, version, help layout, aliases, autocorrect)",
    "strict": "Root.Validate / validation modes not ported",
    "strict_bad": "Root.Validate / validation modes not ported",
    "sig": "signature validators not ported",
    "sig_ok": "signature validators not ported",
    "store": "alias store commands not ported",
    "cfgcmd": "config path|paths commands not ported",
    "cfgnone": "config path|paths commands not ported",
}

#: Vectors inside a replayed tree that are not replayed, with the reason.
SKIPS = {
    "a malformed annotation is not inferred over": (
        "divergence: Go runs the leaf unvalidated here; the port has no Root.Validate, so the "
        "gate fails closed (USAGE) on a malformed kit/side-effect"
    ),
}
_UNSUPPORTED_PREFIX = {
    "chdir": "-C/--chdir not ported",
    "config": "-c/--config layering not ported",
    "flag validator": "flag validators not ported",
}

REPLAYED_TREES = {"ops", "infer"}


def _skip_reason(case: dict[str, Any]) -> str | None:
    if case["tree"] in SKIP_TREES:
        return SKIP_TREES[case["tree"]]
    if case["name"] in SKIPS:
        return SKIPS[case["name"]]
    for prefix, why in _UNSUPPORTED_PREFIX.items():
        if case["name"].startswith(prefix):
            return why
    return None


def test_every_tree_is_classified() -> None:
    assert set(TREES) <= REPLAYED_TREES | set(SKIP_TREES)


def test_skips_name_real_vectors() -> None:
    names = {c["name"] for c in CASES}
    assert set(SKIPS) <= names


def _handler(path: str, flags: list[dict[str, Any]]):
    def run(**kw: Any) -> None:
        typer.echo(
            json.dumps(
                {"dry_run": is_dry_run(), "path": path}, separators=(",", ":"), sort_keys=True
            )
        )

    params = [
        inspect.Parameter(
            f["long"],
            inspect.Parameter.KEYWORD_ONLY,
            default=typer.Option("", f"--{f['long']}", help=f.get("help", "")),
            annotation=str,
        )
        for f in flags
    ]
    run.__signature__ = inspect.Signature(params)  # type: ignore[attr-defined]
    return run


def _add(app: typer.Typer, cmds: list[dict[str, Any]], prefix: tuple[str, ...]) -> None:
    for c in cmds:
        path = (*prefix, c["name"])
        if c.get("commands"):
            sub = typer.Typer()
            _add(sub, c["commands"], path)
            app.add_typer(sub, name=c["name"], help=c.get("about", ""))
            continue
        ann = dict(c.get("annotations", {}))
        if c.get("side_effect"):
            ann["kit/side-effect"] = c["side_effect"]
        if c.get("dry_run"):
            ann["kit/dry-run"] = c["dry_run"]
        fn = annotate(ann)(_handler(" ".join(path), c.get("flags", [])))
        app.command(c["name"], help=c.get("about", ""))(fn)


def _build(tree: dict[str, Any], prompt: PromptSource) -> typer.Typer:
    app, _ = create_app(
        name=tree["program"],
        version=tree["version"],
        help=tree["about"],
        policy_loader=default_policy_loader(tree["program"]) if tree.get("policy") else None,
        prompt_source=prompt,
    )
    _add(app, tree["commands"], ())
    return app


def _materialize(tree: dict[str, Any], tmp: Path) -> None:
    for rel, content in tree.get("fixtures", {}).items():
        target = tmp / rel
        if rel.endswith("/"):
            target.mkdir(parents=True, exist_ok=True)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_vector(case: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    why = _skip_reason(case)
    if why is not None:
        pytest.skip(why)
    assert case["tree"] in REPLAYED_TREES, f"{case['name']}: unclassified tree {case['tree']}"
    tree = TREES[case["tree"]]
    _materialize(tree, tmp_path)
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path / "xdg"))
    for k, v in case.get("env", {}).items():
        monkeypatch.setenv(k, v.replace("{tmp}", str(tmp_path)))
    ans = case.get("prompt_answer")
    prompt = PromptSource.from_answers([ans]) if ans is not None else PromptSource.none()
    args = [a.replace("{tmp}", str(tmp_path)) for a in case["args"]]

    r = CliRunner().invoke(_build(tree, prompt), args)

    assert r.exit_code == case["exit"], (r.stdout, r.stderr)
    if case.get("stdout") is not None:
        assert r.stdout == case["stdout"]
    for s in case.get("stdout_contains", []):
        assert s in r.stdout
    for s in case.get("stdout_omits", []):
        assert s not in r.stdout
    for s in case.get("stderr_contains", []):
        assert s in r.stderr, r.stderr
    if case.get("stderr_json"):
        got = json.loads(r.stderr)
        for k, v in case["stderr_json"].items():
            assert got[k] == v
