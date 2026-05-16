"""Tests for hop_top_kit.hint — mirrors Go's output/hint_test.go."""

import io

from hop_top_kit.hint import (
    Hint,
    HintSet,
    active,
    hints_enabled,
    register_upgrade_hints,
    register_version_hints,
    render_hints,
)

# ---------------------------------------------------------------------------
# HintSet
# ---------------------------------------------------------------------------


class TestHintSet:
    def test_lookup_empty_when_nothing_registered(self):
        s = HintSet()
        assert s.lookup("foo") == []

    def test_register_and_lookup_roundtrip(self):
        s = HintSet()
        s.register("foo", Hint(message="do something"))
        result = s.lookup("foo")
        assert len(result) == 1
        assert result[0].message == "do something"

    def test_register_accumulates_multiple_hints(self):
        s = HintSet()
        s.register("foo", Hint(message="a"), Hint(message="b"))
        s.register("foo", Hint(message="c"))
        assert len(s.lookup("foo")) == 3

    def test_lookup_returns_independent_copy(self):
        s = HintSet()
        s.register("foo", Hint(message="x"))
        copy = s.lookup("foo")
        copy.append(Hint(message="injected"))
        assert len(s.lookup("foo")) == 1

    def test_different_commands_are_independent(self):
        s = HintSet()
        s.register("a", Hint(message="hint-a"))
        s.register("b", Hint(message="hint-b"))
        assert s.lookup("a")[0].message == "hint-a"
        assert s.lookup("b")[0].message == "hint-b"


# ---------------------------------------------------------------------------
# active
# ---------------------------------------------------------------------------


class TestActive:
    def test_returns_all_when_conditions_none(self):
        hints = [Hint(message="a"), Hint(message="b")]
        assert len(active(hints)) == 2

    def test_filters_false_conditions(self):
        hints = [
            Hint(message="yes", condition=lambda: True),
            Hint(message="no", condition=lambda: False),
        ]
        result = active(hints)
        assert len(result) == 1
        assert result[0].message == "yes"

    def test_empty_input(self):
        assert active([]) == []


# ---------------------------------------------------------------------------
# hints_enabled
# ---------------------------------------------------------------------------


class TestHintsEnabled:
    def test_true_by_default(self):
        assert hints_enabled() is True

    def test_false_when_no_hints(self):
        assert hints_enabled(no_hints=True) is False

    def test_false_when_quiet(self):
        assert hints_enabled(quiet=True) is False

    def test_false_when_hints_config_disabled(self):
        assert hints_enabled(hints_config_enabled=False) is False

    def test_true_when_hints_config_enabled(self):
        assert hints_enabled(hints_config_enabled=True) is True

    def test_false_when_HOP_QUIET_HINTS_1(self, monkeypatch):
        monkeypatch.setenv("HOP_QUIET_HINTS", "1")
        assert hints_enabled() is False

    def test_false_when_HOP_QUIET_HINTS_true(self, monkeypatch):
        monkeypatch.setenv("HOP_QUIET_HINTS", "true")
        assert hints_enabled() is False

    def test_false_when_HOP_QUIET_HINTS_yes(self, monkeypatch):
        monkeypatch.setenv("HOP_QUIET_HINTS", "yes")
        assert hints_enabled() is False

    def test_true_when_HOP_QUIET_HINTS_0(self, monkeypatch):
        monkeypatch.setenv("HOP_QUIET_HINTS", "0")
        assert hints_enabled() is True


# ---------------------------------------------------------------------------
# render_hints
# ---------------------------------------------------------------------------


class _FakeTTY(io.StringIO):
    """StringIO that reports isatty() = True."""

    def isatty(self) -> bool:
        return True


class _FakeNonTTY(io.StringIO):
    def isatty(self) -> bool:
        return False


class TestRenderHints:
    def test_noop_for_json(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="x")], "json", no_color=True)
        assert w.getvalue() == ""

    def test_noop_for_yaml(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="x")], "yaml", no_color=True)
        assert w.getvalue() == ""

    def test_noop_when_no_hints(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="x")], "table", no_hints=True, no_color=True)
        assert w.getvalue() == ""

    def test_noop_when_quiet(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="x")], "table", quiet=True, no_color=True)
        assert w.getvalue() == ""

    def test_noop_when_empty_hints(self):
        w = _FakeTTY()
        render_hints(w, [], "table", no_color=True)
        assert w.getvalue() == ""

    def test_noop_when_not_a_tty(self):
        w = _FakeNonTTY()
        render_hints(w, [Hint(message="x")], "table", no_color=True)
        assert w.getvalue() == ""

    def test_renders_hint_with_arrow_prefix(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="do this next")], "table", no_color=True)
        assert "→ do this next" in w.getvalue()

    def test_renders_leading_blank_line(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="x")], "table", no_color=True)
        assert w.getvalue().startswith("\n")

    def test_renders_multiple_active_hints(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="a"), Hint(message="b")], "table", no_color=True)
        assert "→ a" in w.getvalue()
        assert "→ b" in w.getvalue()

    def test_skips_false_condition_hints(self):
        w = _FakeTTY()
        render_hints(
            w,
            [
                Hint(message="yes", condition=lambda: True),
                Hint(message="no", condition=lambda: False),
            ],
            "table",
            no_color=True,
        )
        assert "→ yes" in w.getvalue()
        assert "→ no" not in w.getvalue()

    def test_includes_ansi_when_color_enabled(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="colored")], "table", no_color=False)
        assert "\x1b[" in w.getvalue()

    def test_strips_ansi_when_no_color(self):
        w = _FakeTTY()
        render_hints(w, [Hint(message="plain")], "table", no_color=True)
        assert "\x1b[" not in w.getvalue()

    def test_strips_ansi_when_NO_COLOR_env(self, monkeypatch):
        monkeypatch.setenv("NO_COLOR", "1")
        w = _FakeTTY()
        render_hints(w, [Hint(message="plain")], "table", no_color=False)
        assert "\x1b[" not in w.getvalue()


# ---------------------------------------------------------------------------
# Standard factories
# ---------------------------------------------------------------------------


class TestRegisterUpgradeHints:
    def test_registers_for_upgrade_command(self):
        s = HintSet()
        register_upgrade_hints(s, "mytool", lambda: False)
        assert len(s.lookup("upgrade")) == 1

    def test_inactive_when_not_upgraded(self):
        s = HintSet()
        register_upgrade_hints(s, "mytool", lambda: False)
        assert active(s.lookup("upgrade")) == []

    def test_active_when_upgraded(self):
        s = HintSet()
        upgraded = False
        register_upgrade_hints(s, "mytool", lambda: upgraded)
        upgraded = True
        # Use a mutable container to avoid closure rebind issue.
        s2 = HintSet()
        state = {"v": False}
        register_upgrade_hints(s2, "mytool", lambda: state["v"])
        state["v"] = True
        result = active(s2.lookup("upgrade"))
        assert len(result) == 1
        assert "mytool version" in result[0].message

    def test_message_contains_binary_and_version(self):
        s = HintSet()
        register_upgrade_hints(s, "myprog", lambda: True)
        result = active(s.lookup("upgrade"))
        assert "myprog version" in result[0].message


class TestRegisterVersionHints:
    def test_registers_for_version_command(self):
        s = HintSet()
        register_version_hints(s, "mytool", lambda: False)
        assert len(s.lookup("version")) == 1

    def test_inactive_when_no_update(self):
        s = HintSet()
        register_version_hints(s, "mytool", lambda: False)
        assert active(s.lookup("version")) == []

    def test_active_when_update_available(self):
        s = HintSet()
        state = {"v": False}
        register_version_hints(s, "mytool", lambda: state["v"])
        state["v"] = True
        result = active(s.lookup("version"))
        assert len(result) == 1
        assert "mytool upgrade" in result[0].message

    def test_message_contains_binary_and_upgrade(self):
        s = HintSet()
        register_version_hints(s, "myprog", lambda: True)
        result = active(s.lookup("version"))
        assert "myprog upgrade" in result[0].message


# ---------------------------------------------------------------------------
# create_app integration — --no-hints flag
# ---------------------------------------------------------------------------


class TestCreateAppNoHints:
    def test_no_hints_registered_by_default(self):
        from typer.testing import CliRunner

        from hop_top_kit.cli import create_app

        app, _ = create_app(name="mytool", version="1.0.0", help="A tool")
        result = CliRunner().invoke(app, ["--help"])
        assert "--no-hints" in result.output

    def test_no_hints_absent_when_disable_hints(self):
        from typer.testing import CliRunner

        from hop_top_kit.cli import Disable, create_app

        app, _ = create_app(
            name="mytool", version="1.0.0", help="A tool", disable=Disable(hints=True)
        )
        result = CliRunner().invoke(app, ["--help"])
        assert "--no-hints" not in result.output

    def test_no_hints_survives_when_disable_format(self):
        """Hints decoupled from format — --no-hints survives."""
        from typer.testing import CliRunner

        from hop_top_kit.cli import Disable, create_app

        app, _ = create_app(
            name="mytool", version="1.0.0", help="A tool", disable=Disable(format=True)
        )
        result = CliRunner().invoke(app, ["--help"])
        assert "--no-hints" in result.output
