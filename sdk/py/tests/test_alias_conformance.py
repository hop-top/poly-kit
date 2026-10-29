"""Runs the shared alias corpus in ``contracts/alias-v1``.

The vectors are the source of truth: a failure means this port drifted
from the contract, not that a vector should be edited to fit. Strict:
unknown fields fail, ``version`` must be ``v1`` and every JSON file in
the corpus must have a runner here.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import click
import pytest
from click.shell_completion import ShellComplete
from click.testing import CliRunner

from hop_top_kit.alias import (
    AliasNameError,
    bridge_to_click,
    detect_cycles,
    validate_alias_name,
)

PORT = "py"
FILES = {"bridge.json", "cycles.json", "validate.json"}


def _corpus() -> Path:
    here = Path(__file__).resolve().parent
    for candidate in (here, *here.parents):
        target = candidate / "contracts" / "alias-v1"
        if target.is_dir():
            return target
    raise FileNotFoundError(f"contracts/alias-v1: not found walking up from {here}")


def _strict(obj: dict[str, Any], allowed: set[str], where: str) -> None:
    unknown = set(obj) - allowed
    assert not unknown, f"{where}: unknown fields {sorted(unknown)}"


def _load(name: str, top: set[str] = frozenset()) -> dict[str, Any]:
    corpus = _corpus()
    for p in corpus.glob("*.json"):
        assert p.name in FILES, f"{p.name} has no runner"
    data = json.loads((corpus / name).read_text())
    _strict(data, {"version", "description", "cases", *top}, name)
    assert data["version"] == "v1", f"{name}: version {data['version']}"
    assert data["cases"], f"{name}: cases section is empty"
    return data


# -- validate.json -----------------------------------------------------------

_VALIDATE = _load("validate.json")["cases"]


@pytest.mark.parametrize("case", _VALIDATE, ids=[c["name"] for c in _VALIDATE])
def test_validate(case: dict[str, Any]) -> None:
    _strict(case, {"name", "alias", "builtins", "existing", "expect"}, case["name"])
    got = "ok"
    try:
        validate_alias_name(
            case["alias"],
            builtins=case.get("builtins", ()),
            existing=case.get("existing"),
        )
    except AliasNameError as e:
        got = e.kind
    assert got == case["expect"]


# -- cycles.json -------------------------------------------------------------

_CYCLES = _load("cycles.json")["cases"]


@pytest.mark.parametrize("case", _CYCLES, ids=[c["name"] for c in _CYCLES])
def test_cycles(case: dict[str, Any]) -> None:
    _strict(case, {"name", "aliases", "cycles"}, case["name"])
    assert detect_cycles(case["aliases"]) == case["cycles"]


# -- bridge.json -------------------------------------------------------------

_BRIDGE = _load("bridge.json", {"tree", "describe_template"})


def _build(
    parent: click.Group,
    nodes: list[dict[str, Any]],
    prefix: str,
    seen: dict[str, Any],
    descs: dict[str, str],
) -> None:
    for n in nodes:
        _strict(n, {"name", "description", "flags", "commands"}, n["name"])
        path = f"{prefix} {n['name']}".strip()
        descs[path] = n["description"]
        if n.get("commands"):
            group = click.Group(n["name"], help=n["description"])
            parent.add_command(group)
            _build(group, n["commands"], path, seen, descs)
            continue
        flags = n.get("flags", [])
        has_env = any(f["name"] == "env" for f in flags)

        def leaf(path: str = path, has_env: bool = has_env, **kw: Any) -> None:
            seen.clear()
            seen.update(
                ran="target",
                path=path,
                env=kw.get("env") if has_env else None,
                args=list(kw["args"]),
            )

        params: list[click.Parameter] = [
            click.Option([f"--{f['name']}"], default=f["default"]) for f in flags
        ]
        params.append(click.Argument(["args"], nargs=-1))
        parent.add_command(
            click.Command(n["name"], callback=leaf, params=params, help=n["description"])
        )


def _options(case: dict[str, Any], seen: dict[str, Any]) -> dict[str, Any]:
    o = case.get("options")
    if o is None:
        return {}
    _strict(o, {"hidden", "describe", "help", "dispatch"}, case["name"])
    kw: dict[str, Any] = {}
    if "hidden" in o:
        kw["hidden"] = o["hidden"]
    if o.get("describe"):
        tmpl = _BRIDGE["describe_template"]
        kw["describe"] = lambda name, target: tmpl.replace("{name}", name).replace(
            "{target}", target
        )
    if "help" in o:
        assert o["help"] in ("own", "passthrough"), o["help"]
        kw["help_mode"] = o["help"]
    if o.get("dispatch") == "handler":

        def handler(name: str, target: str, args: list[str]) -> None:
            seen.clear()
            seen.update(ran="handler", name=name, target=target, args=args)

        kw["dispatch"] = handler
    elif o.get("dispatch") == "register-only":
        kw["dispatch"] = False
    elif "dispatch" in o:
        raise AssertionError(f"unknown dispatch {o['dispatch']}")
    return kw


def _setup(case: dict[str, Any]) -> tuple[click.Group, dict[str, Any], dict[str, str]]:
    seen: dict[str, Any] = {}
    descs: dict[str, str] = {}
    root = click.Group(_BRIDGE["tree"]["name"])
    _build(root, _BRIDGE["tree"]["commands"], "", seen, descs)
    aliases = dict(sorted(case["aliases"].items()))
    bridge_to_click(root, aliases, **_options(case, seen))
    for flag in case.get("proxy_flags", []):
        _strict(flag, {"name", "short"}, case["name"])
        for alias in aliases:
            proxy = root.get_command(click.Context(root), alias)
            assert proxy is not None and proxy.name == alias, f"{alias} has no proxy"
            proxy.params.append(
                click.Option([f"-{flag['short']}", f"--{flag['name']}"], help=flag["name"])
            )
    return root, seen, descs


def _params() -> list[Any]:
    out = []
    for case in _BRIDGE["cases"]:
        marks = []
        if PORT in case.get("skip", {}):
            marks.append(pytest.mark.skip(reason=case["skip"][PORT]))
        out.append(pytest.param(case, id=case["name"], marks=marks))
    return out


@pytest.mark.parametrize("case", _params())
def test_bridge(case: dict[str, Any]) -> None:
    _strict(case, {"name", "aliases", "options", "skip", "proxy_flags", "checks"}, case["name"])
    runner = CliRunner()
    for raw in case["checks"]:
        _strict(
            raw,
            {"alias", "argv", "entry", "run", "help", "complete", "candidates", "by_port"},
            case["name"],
        )
        chk = {**raw, **raw.get("by_port", {}).get(PORT, {})}
        root, seen, descs = _setup(case)

        if "entry" in chk:
            alias, want = chk["alias"], chk["entry"]
            cmd = root.get_command(click.Context(root), alias)
            assert cmd is not None, alias
            kind = "proxy" if cmd.name == alias else "native"
            assert kind == want["kind"], alias
            if kind == "proxy":
                if "hidden" in want:
                    assert cmd.hidden == want["hidden"], alias
                if "description" in want:
                    assert cmd.help == want["description"], alias
                    assert cmd.short_help == want["description"], alias
        elif "run" in chk:
            argv, want = chk["argv"], chk["run"]
            res = runner.invoke(root, argv, catch_exceptions=False)
            assert res.exit_code == 0, f"{argv}: {res.output}"
            assert seen.get("ran", "none") == want["ran"], argv
            if want["ran"] == "target":
                got = {k: seen[k] for k in ("path", "env", "args")}
                assert got == {k: want[k] for k in ("path", "env", "args")}, argv
            elif want["ran"] == "handler":
                got = {k: seen[k] for k in ("name", "target", "args")}
                assert got == {k: want[k] for k in ("name", "target", "args")}, argv
        elif "help" in chk:
            argv = chk["argv"]
            res = runner.invoke(root, argv, catch_exceptions=False)
            assert res.exit_code == 0, f"{argv}: {res.output}"
            alias = argv[0]
            cmd = root.get_command(click.Context(root), alias)
            got = "unknown"
            if descs[case["aliases"][alias]] in res.output:
                got = "target"
            elif cmd is not None and cmd.name == alias and cmd.help in res.output:
                got = "alias"
            assert got == chk["help"], res.output
            assert "ran" not in seen, argv
        elif "complete" in chk:
            *args, incomplete = chk["complete"]
            comp = ShellComplete(root, {}, root.name, "_APP_COMPLETE")
            got = sorted(i.value for i in comp.get_completions(args, incomplete))
            assert got == sorted(chk["candidates"]), chk["complete"]
        else:
            raise AssertionError(f"{case['name']}: check has no observable")
