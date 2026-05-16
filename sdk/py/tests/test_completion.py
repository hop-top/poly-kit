"""Tests for hop_top_kit.completion — dynamic value completion system."""

from __future__ import annotations

from hop_top_kit.completion import (
    CompletionItem,
    CompletionRegistry,
    config_keys_completer,
    dir_completer,
    file_completer,
    func_completer,
    prefixed_completer,
    static_completer,
    static_values,
)

# ---------------------------------------------------------------------------
# Static completers
# ---------------------------------------------------------------------------


class TestStaticCompleter:
    def test_all_returned_when_prefix_empty(self):
        c = static_completer(
            CompletionItem("alpha", "first"),
            CompletionItem("bravo", "second"),
        )
        results = c.complete("")
        assert [r.value for r in results] == ["alpha", "bravo"]

    def test_filter_by_prefix(self):
        c = static_completer(
            CompletionItem("alpha"),
            CompletionItem("bravo"),
            CompletionItem("apex"),
        )
        results = c.complete("a")
        assert [r.value for r in results] == ["alpha", "apex"]

    def test_case_insensitive(self):
        c = static_completer(
            CompletionItem("Alpha"),
            CompletionItem("bravo"),
        )
        results = c.complete("al")
        assert [r.value for r in results] == ["Alpha"]

        results = c.complete("AL")
        assert [r.value for r in results] == ["Alpha"]

    def test_descriptions_preserved(self):
        c = static_completer(
            CompletionItem("leo", "Low Earth Orbit"),
        )
        results = c.complete("")
        assert results[0].description == "Low Earth Orbit"


class TestStaticValues:
    def test_strings_become_items(self):
        c = static_values("foo", "bar", "baz")
        results = c.complete("b")
        assert [r.value for r in results] == ["bar", "baz"]

    def test_empty_prefix(self):
        c = static_values("x", "y")
        assert len(c.complete("")) == 2


# ---------------------------------------------------------------------------
# Func completer
# ---------------------------------------------------------------------------


class TestFuncCompleter:
    def test_callback_invoked(self):
        calls = []

        def cb(prefix: str) -> list[CompletionItem]:
            calls.append(prefix)
            return [CompletionItem(f"result-{prefix}")]

        c = func_completer(cb)
        results = c.complete("test")
        assert calls == ["test"]
        assert results[0].value == "result-test"

    def test_empty_return(self):
        c = func_completer(lambda p: [])
        assert c.complete("x") == []


# ---------------------------------------------------------------------------
# Prefixed completer
# ---------------------------------------------------------------------------


class TestPrefixedCompleter:
    def test_dimension_prefix_added(self):
        inner = static_values("alpha", "bravo")
        c = prefixed_completer("env", inner)
        results = c.complete("")
        assert [r.value for r in results] == ["env:alpha", "env:bravo"]

    def test_strips_dimension_from_prefix(self):
        inner = static_values("alpha", "bravo")
        c = prefixed_completer("env", inner)
        results = c.complete("env:a")
        assert [r.value for r in results] == ["env:alpha"]

    def test_no_match_after_dimension(self):
        inner = static_values("alpha")
        c = prefixed_completer("env", inner)
        results = c.complete("env:z")
        assert results == []


# ---------------------------------------------------------------------------
# Config keys completer
# ---------------------------------------------------------------------------


class TestConfigKeysCompleter:
    def test_returns_dict_keys(self):
        cfg = {"debug": True, "verbose": False, "version": "1.0"}
        c = config_keys_completer(cfg)
        results = c.complete("")
        values = [r.value for r in results]
        assert "debug" in values
        assert "verbose" in values
        assert "version" in values

    def test_filters_by_prefix(self):
        cfg = {"debug": True, "verbose": False, "version": "1.0"}
        c = config_keys_completer(cfg)
        results = c.complete("v")
        values = [r.value for r in results]
        assert "debug" not in values
        assert "verbose" in values
        assert "version" in values

    def test_empty_dict(self):
        c = config_keys_completer({})
        assert c.complete("") == []


# ---------------------------------------------------------------------------
# File completer
# ---------------------------------------------------------------------------


class TestFileCompleter:
    def test_returns_matching_files(self, tmp_path):
        (tmp_path / "notes.txt").touch()
        (tmp_path / "data.csv").touch()
        (tmp_path / "readme.md").touch()

        c = file_completer(".txt", ".csv")
        results = c.complete(str(tmp_path) + "/")
        values = [r.value for r in results]
        assert any(v.endswith("notes.txt") for v in values)
        assert any(v.endswith("data.csv") for v in values)
        assert not any(v.endswith("readme.md") for v in values)

    def test_no_extension_filter_returns_all(self, tmp_path):
        (tmp_path / "a.txt").touch()
        (tmp_path / "b.py").touch()
        c = file_completer()
        results = c.complete(str(tmp_path) + "/")
        assert len(results) >= 2


# ---------------------------------------------------------------------------
# Dir completer
# ---------------------------------------------------------------------------


class TestDirCompleter:
    def test_returns_directories(self, tmp_path):
        (tmp_path / "subdir").mkdir()
        (tmp_path / "file.txt").touch()
        c = dir_completer()
        results = c.complete(str(tmp_path) + "/")
        values = [r.value for r in results]
        assert any("subdir" in v for v in values)
        assert not any("file.txt" in v for v in values)


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------


class TestCompletionRegistry:
    def test_register_and_lookup_flag(self):
        reg = CompletionRegistry()
        c = static_values("leo", "geo")
        reg.register("--orbit", c)
        assert reg.for_flag("--orbit") is c

    def test_unknown_flag_returns_none(self):
        reg = CompletionRegistry()
        assert reg.for_flag("--nope") is None

    def test_register_and_lookup_arg(self):
        reg = CompletionRegistry()
        c = static_values("starman", "crew-dragon")
        reg.register_arg("launch", 0, c)
        assert reg.for_arg("launch", 0) is c

    def test_unknown_arg_returns_none(self):
        reg = CompletionRegistry()
        assert reg.for_arg("launch", 5) is None

    def test_multiple_flags(self):
        reg = CompletionRegistry()
        a = static_values("a")
        b = static_values("b")
        reg.register("--flag-a", a)
        reg.register("--flag-b", b)
        assert reg.for_flag("--flag-a") is a
        assert reg.for_flag("--flag-b") is b


# ---------------------------------------------------------------------------
# Click bridge
# ---------------------------------------------------------------------------


class TestClickBridge:
    def test_to_click_completer(self):
        """Verify bridge returns Click CompletionItems."""
        from hop_top_kit.completion import to_click_shell_complete

        c = static_values("leo", "geo", "lunar")
        fn = to_click_shell_complete(c)
        # Click signature: (ctx, param, incomplete) -> list
        results = fn(None, None, "l")
        assert len(results) == 2  # leo, lunar
        # Should be click.shell_completion.CompletionItem instances
        from click.shell_completion import CompletionItem as ClickCI

        assert all(isinstance(r, ClickCI) for r in results)
