"""Unit tests for hop_top_kit.upgrade: lenient semver, Registry, document
migrator behaviour the shared corpus does not pin, and the public import
surface of the release checker."""

from __future__ import annotations

import json

import pytest

import hop_top_kit
import hop_top_kit.upgrade as upgrade
from hop_top_kit.upgrade import (
    DocInvalidError,
    DocOptions,
    DocStep,
    Migration,
    Registry,
    compare_versions,
    default_registry,
    migrate_document,
    migrate_document_map,
    register_migration,
    reset_registry_for_test,
)


def _noop() -> None:
    return None


class TestSemver:
    def test_lenient_not_packaging(self) -> None:
        # packaging.version rejects both of these.
        assert compare_versions("1.0.0-alpha.beta", "1.0.0-beta") < 0
        assert compare_versions("1.x.0", "1.0.0") == 0
        assert compare_versions("1.2.3.4.5", "1.2.3") == 0

    def test_prerelease_text_is_bytewise(self) -> None:
        assert compare_versions("1.0.0-B", "1.0.0-a") < 0

    def test_only_one_leading_v_dropped(self) -> None:
        assert compare_versions("vv1.0.0", "0.0.0") == 0

    def test_padded_number_is_text(self) -> None:
        assert compare_versions("1.0.0-01", "1.0.0-1") != 0


class TestRegistry:
    def test_register_rejects_missing_up(self) -> None:
        with pytest.raises(ValueError, match=r"1\.0\.0"):
            Registry().register(Migration(version="1.0.0", schema="db", up=None))  # type: ignore[arg-type]

    def test_pending_ascending_and_bounded(self) -> None:
        reg = Registry()
        for v in ("1.2.0", "1.0.0", "1.1.0", "2.0.0"):
            reg.register(Migration(version=v, schema="db", up=_noop))
        reg.register(Migration(version="1.1.5", schema="other", up=_noop))
        got = [m.version for m in reg.pending("db", "1.0.0", "1.2.0")]
        assert got == ["1.1.0", "1.2.0"]

    def test_own_registry_ignores_default(self) -> None:
        reset_registry_for_test()
        try:
            register_migration(Migration(version="1.0.0", schema="db", up=_noop))
            assert [m.version for m in default_registry().pending("db", "", "9.9.9")] == ["1.0.0"]
            assert Registry().pending("db", "", "9.9.9") == []
        finally:
            reset_registry_for_test()

    def test_register_registers_default_instance_is_stable(self) -> None:
        assert default_registry() is default_registry()


class TestDocument:
    steps = [DocStep(from_version=1, to_version=2, apply=lambda d: {**d, "a": 1})]

    def test_text_input_and_exact_numbers(self) -> None:
        out, rep = migrate_document('{"version": 1, "n": 12345678901234567890.5}', self.steps)
        assert rep.to_version == 2
        assert '"n":12345678901234567890.5' in out.decode()

    def test_bool_is_not_a_version(self) -> None:
        with pytest.raises(upgrade.DocVersionError):
            migrate_document(b'{"version": true}', self.steps)

    def test_not_json_is_invalid(self) -> None:
        with pytest.raises(DocInvalidError):
            migrate_document(b"{nope", self.steps)
        with pytest.raises(DocInvalidError):
            migrate_document(b'{"version": NaN}', self.steps)

    def test_map_form_never_touches_caller(self) -> None:
        doc = {"version": 1, "k": {"x": 1}}
        out, _ = migrate_document_map(doc, [DocStep(1, 2, lambda d: d | {"y": d["k"].pop("x")})])
        assert doc == {"version": 1, "k": {"x": 1}}
        assert out["y"] == 1

    def test_options_default_version_field(self) -> None:
        out, _ = migrate_document(b'{"version": 1}', self.steps, DocOptions())
        assert json.loads(out) == {"version": 2, "a": 1}


class TestCheckerSurface:
    def test_names_importable_unchanged(self) -> None:
        for name in (
            "Checker",
            "CheckerOptions",
            "UpgradeCheckError",
            "UpgradeCheckResult",
            "create_checker",
        ):
            assert hasattr(upgrade, name)
        assert hop_top_kit.CheckerOptions is upgrade.CheckerOptions
        assert hop_top_kit.create_checker is upgrade.create_checker
