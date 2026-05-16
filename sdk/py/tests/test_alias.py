"""Tests for hop_top_kit.alias — YAML-file-based alias expansion."""

from __future__ import annotations

import click
import pytest

from hop_top_kit.alias import (
    Config,
    Expander,
    bridge_to_click,
    load_from,
    save_to,
)


def _write_config(tmp_path, name: str, expansion: str) -> str:
    cfg_dir = tmp_path / ".tool"
    cfg_dir.mkdir(parents=True, exist_ok=True)
    p = cfg_dir / "config.yaml"
    p.write_text(f"aliases:\n  {name}: {expansion}\n")
    return str(p)


def _expander(local_path: str) -> Expander:
    return Expander(
        Config(
            global_path="/nonexistent/global.yaml",
            local_path=local_path,
            seeded_aliases={"setup": "config interactive"},
            builtins={"task", "config", "help"},
        )
    )


# -- Expand ----------------------------------------------------------------


class TestExpandNoMatch:
    def test_no_match(self, tmp_path):
        p = _write_config(tmp_path, "tl", "task list")
        e = _expander(p)
        args = ["tool", "task", "show", "T-0001"]
        got, ok = e.expand(args)
        assert not ok
        assert got == args


class TestExpandMatch:
    def test_match(self, tmp_path):
        p = _write_config(tmp_path, "tl", "task list")
        e = _expander(p)
        got, ok = e.expand(["tool", "tl"])
        assert ok
        assert got == ["tool", "task", "list"]


class TestExpandMatchWithExtraArgs:
    def test_extra_args(self, tmp_path):
        p = _write_config(tmp_path, "tl", "task list")
        e = _expander(p)
        got, ok = e.expand(["tool", "tl", "--status", "OPEN"])
        assert ok
        assert got == ["tool", "task", "list", "--status", "OPEN"]


class TestExpandFlagBeforeAlias:
    def test_flag_before(self, tmp_path):
        p = _write_config(tmp_path, "tl", "task list")
        e = _expander(p)
        got, ok = e.expand(["tool", "-c", "/tmp/config.yaml", "tl"])
        assert ok
        assert got == ["tool", "-c", "/tmp/config.yaml", "task", "list"]


class TestExpandEmpty:
    def test_empty(self):
        e = _expander("/nonexistent")
        args = ["tool"]
        got, ok = e.expand(args)
        assert not ok
        assert got == args


class TestExpandSeededAlias:
    def test_seeded(self):
        e = _expander("/nonexistent")
        got, ok = e.expand(["tool", "setup"])
        assert ok
        assert got == ["tool", "config", "interactive"]

    def test_seeded_with_trailing(self):
        e = _expander("/nonexistent")
        got, ok = e.expand(["tool", "setup", "--verbose"])
        assert ok
        assert got == ["tool", "config", "interactive", "--verbose"]


# -- Expand with flags ----------------------------------------------------


class TestExpandWithFlags:
    def test_alias_with_flag_defaults(self, tmp_path):
        p = _write_config(tmp_path, "dp", "deploy --env prod --dry-run")
        e = _expander(p)
        got, ok = e.expand(["tool", "dp", "starman"])
        assert ok
        assert got == ["tool", "deploy", "--env", "prod", "--dry-run", "starman"]

    def test_user_overrides_alias_flag(self, tmp_path):
        p = _write_config(tmp_path, "dp", "deploy --env prod")
        e = _expander(p)
        got, ok = e.expand(["tool", "dp", "starman", "--env", "staging"])
        assert ok
        assert got == [
            "tool",
            "deploy",
            "--env",
            "prod",
            "starman",
            "--env",
            "staging",
        ]

    def test_alias_with_bool_flag_negation(self, tmp_path):
        p = _write_config(tmp_path, "dp", "deploy --dry-run")
        e = _expander(p)
        got, ok = e.expand(["tool", "dp", "starman", "--no-dry-run"])
        assert ok
        assert got == [
            "tool",
            "deploy",
            "--dry-run",
            "starman",
            "--no-dry-run",
        ]

    def test_preserves_extra_args(self, tmp_path):
        p = _write_config(tmp_path, "ml", "mission list")
        e = _expander(p)
        got, ok = e.expand(["tool", "ml", "--format", "json"])
        assert ok
        assert got == ["tool", "mission", "list", "--format", "json"]

    def test_alias_no_flags_user_adds_flags(self, tmp_path):
        p = _write_config(tmp_path, "d", "deploy")
        e = _expander(p)
        got, ok = e.expand(["tool", "d", "starman", "--env", "prod", "--dry-run"])
        assert ok
        assert got == [
            "tool",
            "deploy",
            "starman",
            "--env",
            "prod",
            "--dry-run",
        ]


# -- Load (merge priority) ------------------------------------------------


class TestLoadMergesPriority:
    def test_merge(self, tmp_path):
        global_path = str(tmp_path / "global" / "config.yaml")
        save_to(global_path, {"tl": "task list", "setup": "global-override"})

        local_path = str(tmp_path / "local" / "config.yaml")
        save_to(local_path, {"tl": "task list --mine"})

        e = Expander(
            Config(
                global_path=global_path,
                local_path=local_path,
                seeded_aliases={"setup": "config interactive"},
            )
        )
        m = e.load()
        # local overrides global
        assert m["tl"] == "task list --mine"
        # global overrides seeded
        assert m["setup"] == "global-override"


# -- ValidateName ----------------------------------------------------------


class TestValidateName:
    def test_empty(self):
        e = _expander("/nonexistent")
        with pytest.raises(ValueError, match="empty"):
            e.validate_name("")

    def test_whitespace(self):
        e = _expander("/nonexistent")
        with pytest.raises(ValueError, match="whitespace"):
            e.validate_name("a b")

    def test_builtin(self):
        e = _expander("/nonexistent")
        with pytest.raises(ValueError, match="conflicts"):
            e.validate_name("task")

    def test_ok(self):
        e = _expander("/nonexistent")
        e.validate_name("tl")  # no error


# -- LoadFrom / SaveTo ----------------------------------------------------


class TestLoadFromNotExist:
    def test_not_exist(self):
        with pytest.raises(FileNotFoundError):
            load_from("/nonexistent/config.yaml")


class TestSaveToRoundTrip:
    def test_round_trip(self, tmp_path):
        p = str(tmp_path / "cfg" / "config.yaml")
        save_to(p, {"x": "y"})
        got = load_from(p)
        assert got["x"] == "y"


class TestSaveToPreservesOtherKeys:
    def test_preserves(self, tmp_path):
        p = tmp_path / "config.yaml"
        p.write_text("other: value\n")
        save_to(str(p), {"x": "y"})
        data = p.read_text()
        assert "other: value" in data
        assert "x:" in data


class TestSaveToEmptyRemovesSection:
    def test_removes(self, tmp_path):
        p = str(tmp_path / "config.yaml")
        save_to(p, {"x": "y"})
        save_to(p, {})
        data = open(p).read()
        assert "aliases" not in data


# -- All returns copy ------------------------------------------------------


class TestAllReturnsCopy:
    def test_copy(self, tmp_path):
        p = _write_config(tmp_path, "tl", "task list")
        e = _expander(p)
        m = e.load()
        m["injected"] = "evil"
        m2 = e.load()
        assert "injected" not in m2


# -- FindFirstNonFlag -----------------------------------------------------


class TestFindFirstNonFlag:
    @pytest.mark.parametrize(
        "name,slc,idx,val",
        [
            ("simple", ["cmd"], 0, "cmd"),
            ("short flag consumes next", ["-v", "cmd"], -1, ""),
            ("flag=val", ["--config=/tmp", "cmd"], 1, "cmd"),
            ("flag val", ["-c", "/tmp", "cmd"], 2, "cmd"),
            ("only flags", ["-v", "--debug"], -1, ""),
        ],
    )
    def test_cases(self, name, slc, idx, val):
        from hop_top_kit.alias import find_first_non_flag

        got_idx, got_val = find_first_non_flag(slc)
        assert got_idx == idx
        assert got_val == val


class TestFindFirstNonFlagBooleanFlag:
    def test_long_flag_without_eq_is_boolean(self):
        """--quiet is boolean; ml should be found as the alias."""
        from hop_top_kit.alias import find_first_non_flag

        idx, val = find_first_non_flag(["--quiet", "ml"])
        assert idx == 1
        assert val == "ml"


# -- bridge_to_click --------------------------------------------------------


class TestBridgeResolvesAlias:
    def test_simple_alias(self, tmp_path):
        @click.group()
        def cli():
            pass

        @cli.command()
        def deploy():
            pass

        save_to(str(tmp_path / "aliases.yaml"), {"d": "deploy"})
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        cmd = cli.get_command(ctx, "d")
        assert cmd is not None
        assert cmd.name == "deploy"

    def test_real_command_takes_precedence(self, tmp_path):
        @click.group()
        def cli():
            pass

        @cli.command()
        def deploy():
            pass

        @cli.command()
        def d():
            pass

        save_to(str(tmp_path / "aliases.yaml"), {"d": "deploy"})
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        cmd = cli.get_command(ctx, "d")
        assert cmd is not None
        assert cmd.name == "d"

    def test_unknown_returns_none(self, tmp_path):
        @click.group()
        def cli():
            pass

        save_to(str(tmp_path / "aliases.yaml"), {"d": "deploy"})
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        assert cli.get_command(ctx, "nope") is None

    def test_missing_file_is_noop(self):
        @click.group()
        def cli():
            pass

        @cli.command()
        def deploy():
            pass

        bridge_to_click(cli, "/nonexistent/aliases.yaml")
        # Should still work, no crash
        ctx = click.Context(cli)
        cmd = cli.get_command(ctx, "deploy")
        assert cmd is not None


class TestBridgeListsAliases:
    def test_includes_alias_names(self, tmp_path):
        @click.group()
        def cli():
            pass

        @cli.command()
        def deploy():
            pass

        @cli.command()
        def status():
            pass

        save_to(
            str(tmp_path / "aliases.yaml"),
            {
                "d": "deploy",
                "s": "status",
            },
        )
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        cmds = cli.list_commands(ctx)
        assert "d" in cmds
        assert "s" in cmds
        assert "deploy" in cmds
        assert "status" in cmds

    def test_sorted(self, tmp_path):
        @click.group()
        def cli():
            pass

        @cli.command()
        def zoo():
            pass

        @cli.command()
        def alpha():
            pass

        save_to(str(tmp_path / "aliases.yaml"), {"m": "zoo"})
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        cmds = cli.list_commands(ctx)
        assert cmds == sorted(cmds)


class TestBridgeMultiWordAlias:
    def test_resolves_first_word(self, tmp_path):
        """Multi-word alias resolves to the first word's command."""

        @click.group()
        def cli():
            pass

        @cli.command()
        def task():
            pass

        save_to(
            str(tmp_path / "aliases.yaml"),
            {"tl": "task list"},
        )
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        ctx = click.Context(cli)
        cmd = cli.get_command(ctx, "tl")
        assert cmd is not None
        assert cmd.name == "task"


class TestBridgeEmptyAliases:
    def test_empty_file(self, tmp_path):
        @click.group()
        def cli():
            pass

        @cli.command()
        def deploy():
            pass

        p = tmp_path / "aliases.yaml"
        p.write_text("aliases: {}\n")
        bridge_to_click(cli, str(p))

        ctx = click.Context(cli)
        cmds = cli.list_commands(ctx)
        assert cmds == ["deploy"]
