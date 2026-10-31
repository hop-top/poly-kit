"""The delegation gate, framework-neutral: dry-run, policy, confirm matrix,
typed token, max-ops, inferred-destructive preview."""

from __future__ import annotations

import hashlib
import io
import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit import gate as g
from hop_top_kit import policy as pol
from hop_top_kit.output.error import CLIError

SE = "kit/side-effect"
NOTTY = g.PromptSource.none()


def _gate(**kw: Any) -> g.Gate:
    kw.setdefault("prompt_source", NOTTY)
    return g.Gate("kitapp", **kw)


def _leaf(*path: str, fmt: str = "", token: str = "", **ann: str) -> g.Leaf:
    return g.Leaf(
        program="kitapp", path=tuple(path), annotations=ann, format=fmt, confirm_token=token
    )


class Probe:
    """A handler that records whether it ran, and whether it saw a dry run."""

    def __init__(self) -> None:
        self.calls: list[bool] = []

    def __call__(self) -> str:
        from hop_top_kit.sideeffect import is_dry_run

        self.calls.append(is_dry_run())
        return "ran"


def _refusal(fn: Callable[[], Any]) -> CLIError:
    with pytest.raises(CLIError) as ei:
        fn()
    return ei.value


def test_token_is_the_first_six_bytes_of_sha256_hex() -> None:
    assert g.destructive_token("kitapp purge") == "9bcc9064589a"
    assert g.destructive_token("kitapp purge") == hashlib.sha256(b"kitapp purge").hexdigest()[:12]
    assert g.destructive_token("kitapp rm") != g.destructive_token("kitapp purge")


def test_confirm_mode_defaults_by_terminal_availability() -> None:
    tty = g.PromptSource.from_answers(["y"])
    M = g.ConfirmMode
    assert g.resolve_confirm_mode(NOTTY, "") is M.NO
    assert g.resolve_confirm_mode(tty, "") is M.PROMPT
    assert g.resolve_confirm_mode(NOTTY, "garbage") is M.NO
    assert g.resolve_confirm_mode(tty, " YES ") is M.YES
    assert g.resolve_confirm_mode(NOTTY, "auto") is M.AUTO
    assert g.resolve_confirm_mode(NOTTY, "prompt") is M.PROMPT


def test_controlling_tty_is_none_without_a_terminal(monkeypatch: pytest.MonkeyPatch) -> None:
    def deny(*a: object, **k: object) -> None:
        raise OSError("no tty")

    monkeypatch.setattr(g, "_tty_cache", [])
    monkeypatch.setattr(g, "_open_tty", deny)
    assert g.PromptSource.controlling_tty().get() is None


# -- confirm matrix ----------------------------------------------------------


def test_destructive_without_a_terminal_is_refused_transient_unauthorized() -> None:
    probe = Probe()
    err = _refusal(lambda: _gate().run(_leaf("rm", **{SE: "destructive"}), g.Globals(), probe))
    assert (err.code, err.exit_code) == ("UNAUTHORIZED", 5)
    assert "refused: --confirm=no (or non-TTY default)" in err.message
    assert err.transience == "transient"
    assert probe.calls == []


@pytest.mark.parametrize("mode", ["yes", "auto"])
def test_confirm_yes_and_auto_let_a_destructive_run(mode: str) -> None:
    probe = Probe()
    _gate().run(_leaf("rm", **{SE: "destructive"}), g.Globals(confirm=mode), probe)
    assert probe.calls == [False]


def test_confirm_no_refuses_even_with_a_terminal() -> None:
    gate = _gate(prompt_source=g.PromptSource.from_answers(["y"]))
    err = _refusal(
        lambda: gate.run(_leaf("rm", **{SE: "destructive"}), g.Globals(confirm="no"), Probe())
    )
    assert "refused: --confirm=no" in err.message


def test_prompt_accepts_y_and_refuses_n() -> None:
    probe = Probe()
    leaf = _leaf("rm", **{SE: "destructive"})
    _gate(prompt_source=g.PromptSource.from_answers(["y"])).run(
        leaf, g.Globals(confirm="prompt"), probe
    )
    assert probe.calls == [False]
    err = _refusal(
        lambda: _gate(prompt_source=g.PromptSource.from_answers(["n"])).run(
            leaf, g.Globals(confirm="prompt"), probe
        )
    )
    assert err.message == "aborted by user at confirm prompt for kitapp rm"
    assert err.transience == "transient"
    assert probe.calls == [False]


def test_unset_confirm_prompts_when_a_terminal_exists() -> None:
    probe = Probe()
    _gate(prompt_source=g.PromptSource.from_answers(["yes"])).run(
        _leaf("rm", **{SE: "destructive"}), g.Globals(), probe
    )
    assert probe.calls == [False]


def test_writes_and_reads_need_no_confirmation() -> None:
    probe = Probe()
    _gate().run(_leaf("add", **{SE: "write"}), g.Globals(), probe)
    _gate().run(_leaf("ls", **{SE: "read"}), g.Globals(), probe)
    assert len(probe.calls) == 2


# -- typed token -------------------------------------------------------------

PURGE = {SE: "destructive", "kit/destructive-token": "required"}


def test_token_required_even_under_confirm_yes() -> None:
    err = _refusal(
        lambda: _gate().run(_leaf("purge", **PURGE), g.Globals(confirm="yes"), Probe(), err=None)
    )
    assert "requires --confirm-token=9bcc9064589a" in err.message
    assert err.exit_code == 5


def test_token_preview_goes_to_stderr_only_in_human_format() -> None:
    buf = io.StringIO()
    _refusal(lambda: _gate().run(_leaf("purge", **PURGE), g.Globals(), Probe(), err=buf))
    assert "Token: 9bcc9064589a" in buf.getvalue()
    buf = io.StringIO()
    e = _refusal(
        lambda: _gate().run(_leaf("purge", fmt="json", **PURGE), g.Globals(), Probe(), err=buf)
    )
    assert buf.getvalue() == ""
    assert e.suggested_fix == "re-run with --confirm-token=9bcc9064589a to proceed"


def test_token_accepted_without_confirm() -> None:
    probe = Probe()
    _gate().run(_leaf("purge", token="9bcc9064589a", **PURGE), g.Globals(), probe)
    assert probe.calls == [False]


def test_token_mismatch_is_refused() -> None:
    probe = Probe()
    err = _refusal(
        lambda: _gate().run(
            _leaf("purge", token="deadbeef0000", **PURGE), g.Globals(confirm="yes"), probe
        )
    )
    assert err.message == "--confirm-token mismatch for kitapp purge; expected 9bcc9064589a"
    assert probe.calls == []


# -- policy ------------------------------------------------------------------


def _loader(p: pol.Policy) -> Callable[[str], pol.Policy]:
    return lambda name: p


def test_policy_refusal_is_final_under_confirm_yes() -> None:
    gate = _gate(policy_loader=_loader(pol.parse("allow:\n  destructive: []\n")))
    probe = Probe()
    err = _refusal(
        lambda: gate.run(
            _leaf("rm", **{SE: "destructive"}), g.Globals(policy="nodel", confirm="yes"), probe
        )
    )
    assert err.message == "policy: destructive not allowed for rm"
    assert err.exit_code == 5 and err.transience == "permanent"
    assert probe.calls == []


def test_policy_require_confirm_lifts_a_write_into_the_gate() -> None:
    gate = _gate(policy_loader=_loader(pol.parse('require_confirm: ["add"]\n')))
    leaf = _leaf("add", **{SE: "write"})
    err = _refusal(lambda: gate.run(leaf, g.Globals(policy="c"), Probe()))
    assert "refused: --confirm=no" in err.message
    gate.run(leaf, g.Globals(policy="c", confirm="yes"), Probe())


def test_policy_loader_errors_are_usage() -> None:
    def loader(name: str) -> pol.Policy:
        raise pol.PolicyError("policy: load /x: no such file or directory")

    err = _refusal(
        lambda: _gate(policy_loader=loader).run(
            _leaf("add", **{SE: "write"}), g.Globals(policy="ghost"), Probe()
        )
    )
    assert (err.code, err.exit_code) == ("USAGE", 2)
    assert err.message.startswith('policy "ghost": ')


def test_policy_without_a_loader_is_usage() -> None:
    err = _refusal(
        lambda: _gate().run(_leaf("add", **{SE: "write"}), g.Globals(policy="x"), Probe())
    )
    assert err.exit_code == 2 and "no policy loader is wired" in err.message


def test_invalid_policy_name_refused_by_the_default_loader(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    gate = _gate(policy_loader=pol.default_policy_loader("kitapp"))
    for bad in ("../x", str(tmp_path / "kitapp" / "policies" / "readonly")):
        err = _refusal(
            lambda b=bad: gate.run(_leaf("add", **{SE: "write"}), g.Globals(policy=b), Probe())
        )
        assert err.exit_code == 2 and "invalid policy name" in err.message


def test_kit_default_needs_no_loader_and_refuses_a_write() -> None:
    err = _refusal(
        lambda: _gate().run(_leaf("add", **{SE: "write"}), g.Globals(policy="kit-default"), Probe())
    )
    assert "policy: write not allowed for add" in err.message
    assert "policy kit-default:" in err.message


def test_policy_exempts_the_supervisor_serve() -> None:
    gate = _gate(policy_loader=_loader(pol.parse("allow:\n  write: []\n")))
    probe = Probe()
    gate.run(_leaf("serve", **{SE: "write"}), g.Globals(policy="p"), probe)
    assert probe.calls == [False]


# -- max-ops -----------------------------------------------------------------


def test_max_ops_budget_is_spent_by_mutating_runs_only() -> None:
    gate = _gate()
    eng = gate.engine(g.Globals(max_ops=2))
    assert eng.max_ops == 2
    w = _leaf("add", **{SE: "write"})
    r = _leaf("ls", **{SE: "read"})
    gate.run(w, g.Globals(max_ops=2), Probe(), engine=eng)
    gate.run(r, g.Globals(max_ops=2), Probe(), engine=eng)
    gate.run(w, g.Globals(max_ops=2), Probe(), engine=eng)
    probe = Probe()
    err = _refusal(lambda: gate.run(w, g.Globals(max_ops=2), probe, engine=eng))
    assert (err.code, err.exit_code) == ("RATE_LIMITED", 64)
    assert err.message == "max-ops budget exceeded after running kitapp add"
    assert eng.ops_count == 3


def test_max_ops_within_budget_runs() -> None:
    probe = Probe()
    _gate().run(_leaf("add", **{SE: "write"}), g.Globals(max_ops=1), probe)
    assert probe.calls == [False]


def test_failed_handler_is_not_counted() -> None:
    gate = _gate()
    eng = gate.engine(g.Globals(max_ops=1))

    def boom() -> None:
        raise RuntimeError("x")

    with pytest.raises(RuntimeError):
        gate.run(_leaf("add", **{SE: "write"}), g.Globals(max_ops=1), boom, engine=eng)
    assert eng.ops_count == 0


# -- dry-run -----------------------------------------------------------------


def test_dry_run_tags_a_write_and_skips_the_destructive_confirm() -> None:
    probe = Probe()
    _gate().run(_leaf("add", **{SE: "write"}), g.Globals(dry_run=True), probe)
    _gate().run(_leaf("rm", **{SE: "destructive"}), g.Globals(dry_run=True), probe)
    assert probe.calls == [True, True]


def test_dry_run_is_not_counted_against_the_budget() -> None:
    gate = _gate()
    eng = gate.engine(g.Globals(max_ops=1))
    for _ in range(3):
        gate.run(_leaf("add", **{SE: "write"}), g.Globals(dry_run=True), Probe(), engine=eng)
    assert eng.ops_count == 0


def test_dry_run_on_a_read_is_a_noop() -> None:
    probe = Probe()
    _gate().run(_leaf("ls", **{SE: "read"}), g.Globals(dry_run=True), probe)
    assert probe.calls == [False]


@pytest.mark.parametrize(
    ("ann", "needle"),
    [
        ({SE: "interactive"}, "does not accept --dry-run: interactive"),
        ({SE: "write", "kit/dry-run": "opted-out"}, "explicitly opted out"),
        ({}, "missing the required kit/side-effect tag"),
        ({SE: "bogus"}, "malformed"),
    ],
)
def test_dry_run_refusals(ann: dict[str, str], needle: str) -> None:
    probe = Probe()
    err = _refusal(lambda: _gate().run(_leaf("shell", **ann), g.Globals(dry_run=True), probe))
    assert (err.code, err.exit_code) == ("USAGE", 2)
    assert needle in err.message
    assert probe.calls == []


def test_dry_run_disabled_ignores_the_flag() -> None:
    probe = Probe()
    _gate(dry_run_disabled=True).run(
        _leaf("shell", **{SE: "interactive"}), g.Globals(dry_run=True), probe
    )
    assert probe.calls == [False]


# -- inference ---------------------------------------------------------------


def test_unannotated_destructive_name_is_gated() -> None:
    probe = Probe()
    err = _refusal(lambda: _gate().run(_leaf("delete"), g.Globals(), probe))
    assert "refused: --confirm=no" in err.message
    err = _refusal(lambda: _gate().run(_leaf("widget", "remove"), g.Globals(), probe))
    assert "refused: --confirm=no" in err.message
    assert probe.calls == []
    _gate().run(_leaf("delete"), g.Globals(confirm="yes"), probe)
    assert probe.calls == [False]


def test_inference_leaves_the_rest_alone() -> None:
    probe = Probe()
    _gate().run(_leaf("list"), g.Globals(), probe)
    _gate().run(_leaf("delete-all"), g.Globals(), probe)
    _gate().run(_leaf("drop", **{SE: "read"}), g.Globals(), probe)
    _gate().run(_leaf("purge", **{SE: "bogus"}), g.Globals(), probe)
    assert len(probe.calls) == 4


def test_policy_sees_the_inferred_tier() -> None:
    gate = _gate(policy_loader=_loader(pol.parse("allow:\n  destructive: []\n")))
    err = _refusal(lambda: gate.run(_leaf("delete"), g.Globals(policy="p", confirm="yes"), Probe()))
    assert err.message == "policy: destructive-shared not allowed for delete"


def test_dry_run_on_an_inferred_leaf_never_invokes_the_handler() -> None:
    """An inferred leaf never promised to honor --dry-run, so its handler
    may really mutate. Kit renders the preview itself; nothing runs, and no
    --confirm is needed because nothing can mutate."""
    mutated: list[str] = []

    def handler() -> None:
        mutated.append("deleted")

    out = io.StringIO()
    result = _gate().run(_leaf("delete"), g.Globals(dry_run=True), handler, out=out)
    assert mutated == []
    assert result is None
    assert "delete" in out.getvalue()


def test_inferred_preview_is_a_plan_in_machine_formats() -> None:
    out = io.StringIO()
    _gate().run(_leaf("widget", "remove", fmt="json"), g.Globals(dry_run=True), Probe(), out=out)
    plan = json.loads(out.getvalue())
    assert plan["command"] == "kitapp widget remove"
    assert plan["effects"][0]["reversible"] is False
    assert plan["warnings"]


def test_inferred_preview_still_honors_a_policy_refusal() -> None:
    gate = _gate(policy_loader=_loader(pol.parse("allow:\n  destructive: []\n")))
    probe = Probe()
    err = _refusal(lambda: gate.run(_leaf("delete"), g.Globals(policy="p", dry_run=True), probe))
    assert err.exit_code == 5
    assert probe.calls == []


def test_inferred_preview_respects_opt_out_and_declared_wins() -> None:
    probe = Probe()
    err = _refusal(
        lambda: _gate().run(
            _leaf("delete", **{"kit/dry-run": "opted-out"}), g.Globals(dry_run=True), probe
        )
    )
    assert "explicitly opted out" in err.message
    # a declared destructive leaf keeps the handler-runs-as-preview contract
    _gate().run(_leaf("delete", **{SE: "destructive"}), g.Globals(dry_run=True), probe)
    assert probe.calls == [True]
