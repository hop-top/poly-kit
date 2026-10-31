"""The delegation policy: engine, loader, name validation, kit-default."""

from __future__ import annotations

from pathlib import Path

import pytest

from hop_top_kit import policy as pol
from hop_top_kit import xdg


def _p(yaml_text: str) -> pol.Policy:
    return pol.parse(yaml_text)


def _ref(verb: str, se: str | None = None, perms: str | None = None) -> pol.CommandRef:
    return pol.CommandRef(verb=verb, side_effect=se, permissions=perms)


def test_empty_policy_permits_everything() -> None:
    e = pol.Engine(pol.Policy())
    for se in ("read", "write", "destructive", "interactive", None):
        assert e.authorize(_ref("x", se)).allowed


def test_empty_allow_list_refuses_the_class() -> None:
    e = pol.Engine(_p("allow:\n  destructive: []\n"))
    v = e.authorize(_ref("rm", "destructive"))
    assert not v.allowed
    assert v.reason == "policy: destructive not allowed for rm"
    assert e.authorize(_ref("add", "write")).allowed
    assert e.authorize(_ref("ls", "read")).allowed


def test_allow_globs_and_colon_shorthand() -> None:
    e = pol.Engine(_p('allow:\n  write: ["widget:*", "sync"]\n'))
    assert e.authorize(_ref("widget add", "write")).allowed
    assert e.authorize(_ref("widget", "write")).allowed
    assert e.authorize(_ref("sync", "write")).allowed
    assert not e.authorize(_ref("other", "write")).allowed


def test_expanded_tiers_fall_back_to_their_class() -> None:
    e = pol.Engine(_p("allow:\n  write: []\n"))
    assert not e.authorize(_ref("a", "write-local")).allowed
    e = pol.Engine(_p('allow:\n  write: []\n  write-local: ["*"]\n'))
    assert e.authorize(_ref("a", "write-local")).allowed
    assert not e.authorize(_ref("a", "write-shared")).allowed


def test_require_confirm_is_reported() -> None:
    e = pol.Engine(_p('require_confirm: ["add"]\n'))
    v = e.authorize(_ref("add", "write"))
    assert v.allowed and v.require_confirm
    assert not e.authorize(_ref("ls", "write")).require_confirm


def test_unannotated_class_applies_to_untagged() -> None:
    e = pol.Engine(_p("unannotated: write\nallow:\n  write: []\n"))
    assert not e.authorize(_ref("x", None)).allowed


def test_max_ops_override_beats_the_policy_and_counts_past_the_cap() -> None:
    e = pol.Engine(_p("max_ops: 5\n"), 2)
    assert e.max_ops == 2
    e.record_op()
    e.record_op()
    with pytest.raises(pol.MaxOpsExceeded):
        e.record_op()
    unlimited = pol.Engine(pol.Policy())
    for _ in range(100):
        unlimited.record_op()
    from_policy = pol.Engine(_p("max_ops: 1\n"))
    from_policy.record_op()
    with pytest.raises(pol.MaxOpsExceeded):
        from_policy.record_op()


def test_kit_default_refuses_a_write_with_the_remedy() -> None:
    e = pol.Engine(pol.kit_default())
    v = e.authorize(_ref("add", "write"))
    assert not v.allowed
    assert v.reason.startswith("policy: write not allowed for add (policy kit-default: ")
    assert not e.authorize(_ref("x", None)).allowed  # untagged is a write under it
    assert e.authorize(_ref("ls", "read")).allowed


@pytest.mark.parametrize(
    "name", ["../x", "a/b", "", "a.b", "x" * 65, "a b", "ok\n", "é", "/abs/path/readonly"]
)
def test_policy_names_are_validated_before_any_fs_access(
    name: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

    def boom(*a: object, **k: object) -> None:
        raise AssertionError("filesystem touched")

    monkeypatch.setattr(Path, "read_text", boom)
    monkeypatch.setattr("builtins.open", boom)
    with pytest.raises(pol.PolicyError) as ei:
        pol.load_named("tool", name)
    assert "policy name" in str(ei.value)


def test_valid_names() -> None:
    for n in ("a", "A_b-9", "x" * 64):
        assert pol.valid_name(n)


def test_resolve_uses_the_xdg_config_dir(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    assert pol.resolve("kitapp", "ro") == Path(xdg.config_dir("kitapp")) / "policies" / "ro.yaml"
    with pytest.raises(pol.PolicyError):
        pol.resolve("", "ro")


def test_load_named_reads_the_file_and_defaults_the_name(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    d = tmp_path / "kitapp" / "policies"
    d.mkdir(parents=True)
    (d / "readonly.yaml").write_text("allow:\n  write: []\n")
    p = pol.load_named("kitapp", "readonly")
    assert p.name == "readonly" and p.allow == {"write": []}
    with pytest.raises(pol.PolicyError, match="no such file or directory"):
        pol.load_named("kitapp", "ghost")
    (d / "broken.yaml").write_text("allow: [\n")
    with pytest.raises(pol.PolicyError, match="parse"):
        pol.load_named("kitapp", "broken")


def test_kit_default_is_reserved_and_reads_no_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    assert pol.load_named("kitapp", "kit-default").name == "kit-default"


def test_validation_refuses_what_go_refuses() -> None:
    with pytest.raises(pol.PolicyError, match="unannotated: unknown side-effect class"):
        _p("unannotated: nope\n")
    with pytest.raises(pol.PolicyError, match=r"callers\[0\]: allow: unknown"):
        _p("callers:\n  - allow:\n      weird: []\n")
    with pytest.raises(pol.PolicyError, match="max_ops -1 is negative"):
        _p("callers:\n  - max_ops: -1\n")
    with pytest.raises(pol.PolicyError, match="otherwise required"):
        _p("permissions:\n  - name: a\n    when: x\n    effect: allow\n")


def test_path_match_is_gos() -> None:
    assert pol.path_match("a*", "abc") is True
    assert pol.path_match("a*", "a/c") is False
    assert pol.path_match("a?c", "abc") is True
    assert pol.path_match("[a-c]x", "bx") is True
    assert pol.path_match("[^a-c]x", "bx") is False
    assert pol.path_match("\\*", "*") is True
    assert pol.path_match("[", "x") is None
