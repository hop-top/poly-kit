"""Tests for hop_top_kit.output.registry — Registry + default_registry."""

from __future__ import annotations

import pytest

from hop_top_kit.output.formatter import OptionSpec
from hop_top_kit.output.registry import Registry, default_registry, new_registry

# ---------------------------------------------------------------------------
# Test helpers
# ---------------------------------------------------------------------------


class _Fmt:
    def __init__(self, key: str, extensions: tuple[str, ...] = ()) -> None:
        self.key = key
        self.extensions = extensions

    def options(self) -> list[OptionSpec]:
        return []

    def render(self, out, data, opts, cols) -> None:  # pragma: no cover
        out.write("ok")


# ---------------------------------------------------------------------------
# register / lookup / override
# ---------------------------------------------------------------------------


def test_register_and_lookup():
    r = new_registry()
    f = _Fmt("json")
    r.register(f)
    assert r.lookup("json") is f
    assert r.lookup("missing") is None


def test_register_duplicate_raises():
    r = new_registry()
    r.register(_Fmt("json"))
    with pytest.raises(ValueError, match="already registered"):
        r.register(_Fmt("json"))


def test_register_empty_key_raises():
    r = new_registry()
    with pytest.raises(ValueError, match="key is empty"):
        r.register(_Fmt(""))


def test_register_non_formatter_raises():
    r = new_registry()

    class _Bad:
        # missing required attrs
        pass

    with pytest.raises(ValueError, match="Formatter protocol"):
        r.register(_Bad())  # type: ignore[arg-type]


def test_override_replaces():
    r = new_registry()
    f1 = _Fmt("json")
    r.register(f1)
    f2 = _Fmt("json")
    r.override(f2)
    assert r.lookup("json") is f2


def test_override_can_register_new():
    r = new_registry()
    f = _Fmt("yaml")
    r.override(f)
    assert r.lookup("yaml") is f


# ---------------------------------------------------------------------------
# keys / formatters / extension_map (stable order)
# ---------------------------------------------------------------------------


def test_keys_sorted():
    r = new_registry()
    r.register(_Fmt("yaml"))
    r.register(_Fmt("json"))
    r.register(_Fmt("table"))
    assert r.keys() == ["json", "table", "yaml"]


def test_formatters_in_key_order():
    r = new_registry()
    r.register(_Fmt("yaml"))
    r.register(_Fmt("json"))
    out = r.formatters()
    assert [f.key for f in out] == ["json", "yaml"]


def test_extension_map_basic():
    r = new_registry()
    r.register(_Fmt("json", (".json",)))
    r.register(_Fmt("yaml", (".yaml", ".yml")))
    r.register(_Fmt("csv", (".csv",)))
    em = r.extension_map()
    assert em == {".json": "json", ".yaml": "yaml", ".yml": "yaml", ".csv": "csv"}


def test_extension_map_lowercases():
    r = new_registry()
    r.register(_Fmt("json", (".JSON",)))
    em = r.extension_map()
    assert em == {".json": "json"}


def test_extension_map_stable_collision_order():
    """When two formatters claim the same ext, later (sorted-key) wins."""
    r = new_registry()
    r.register(_Fmt("aaa", (".x",)))
    r.register(_Fmt("zzz", (".x",)))
    em = r.extension_map()
    # sorted iteration: 'aaa' first then 'zzz' overwrites — 'zzz' wins.
    assert em[".x"] == "zzz"


# ---------------------------------------------------------------------------
# Isolation: new_registry vs default_registry
# ---------------------------------------------------------------------------


def test_isolated_registries():
    r1 = new_registry()
    r2 = new_registry()
    r1.register(_Fmt("custom"))
    assert r2.lookup("custom") is None


def test_default_registry_is_singleton_module_level():
    from hop_top_kit.output import registry as reg_mod

    assert reg_mod.default_registry is default_registry
    assert isinstance(default_registry, Registry)
