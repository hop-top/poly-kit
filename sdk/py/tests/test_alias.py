"""Tests for hop_top_kit.alias — YAML-file-based alias expansion."""

from __future__ import annotations

import click
import pytest
from click.testing import CliRunner

from hop_top_kit.alias import (
    AliasNameError,
    Config,
    Expander,
    bridge_to_click,
    load_from,
    save_to,
    validate_alias_name,
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
    def _cli(self, ran: list[str]):
        @click.group()
        def cli():
            pass

        @cli.group(help="Manage tasks")
        def task():
            ran.append("task")

        @task.command("list", help="List tasks")
        @click.option("--status", default="any")
        @click.argument("args", nargs=-1)
        def task_list(status, args):
            ran.append(f"list {status} {list(args)}")

        return cli

    def test_resolves_to_hidden_proxy(self, tmp_path):
        """Multi-word alias resolves to a hidden proxy named after it."""
        cli = self._cli([])
        save_to(str(tmp_path / "aliases.yaml"), {"tl": "task list"})
        bridge_to_click(cli, str(tmp_path / "aliases.yaml"))

        cmd = cli.get_command(click.Context(cli), "tl")
        assert cmd is not None
        assert cmd.name == "tl"
        assert cmd.hidden is True
        assert cmd.help == "Alias for task list"
        assert "tl" in cli.list_commands(click.Context(cli))

    def test_runs_full_target_path(self):
        """The whole path runs, group callback included, with the args."""
        ran: list[str] = []
        cli = self._cli(ran)
        bridge_to_click(cli, {"tl": "task list"})
        res = CliRunner().invoke(cli, ["tl", "--status", "open", "p1"])
        assert res.exit_code == 0, res.output
        assert ran == ["task", "list open ['p1']"]

    def test_owns_help(self):
        cli = self._cli([])
        bridge_to_click(cli, {"tl": "task list"})
        res = CliRunner().invoke(cli, ["tl", "--help"])
        assert res.exit_code == 0, res.output
        assert "Alias for task list" in res.output
        assert "List tasks" not in res.output

    def test_offered_once_in_completion_not_in_help(self):
        from click.shell_completion import ShellComplete

        cli = self._cli([])
        bridge_to_click(cli, {"tl": "task list"})
        comp = ShellComplete(cli, {}, "cli", "_CLI_COMPLETE")
        got = [i.value for i in comp.get_completions([], "t")]
        assert sorted(got) == ["task", "tl"]
        res = CliRunner().invoke(cli, ["--help"])
        assert "tl" not in res.output.split()

    def test_options_hidden_proxy_completion_unchanged(self):
        from click.shell_completion import ShellComplete

        cli = self._cli([])
        bridge_to_click(cli, {"tl": "task list"}, hidden=True)
        comp = ShellComplete(cli, {}, "cli", "_CLI_COMPLETE")
        assert [i.value for i in comp.get_completions([], "t")] == ["task"]

    def test_single_word_stays_native(self):
        cli = self._cli([])
        bridge_to_click(cli, {"t": "task", "tl": "task list"})
        assert cli.get_command(click.Context(cli), "t").name == "task"


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


# -- validate_alias_name / bridge options ------------------------------------


class TestValidateAliasName:
    @pytest.mark.parametrize(
        ("name", "kw", "text"),
        [
            ("", {}, "alias name must not be empty"),
            ("a b", {}, "alias name must not contain whitespace"),
            ("task", {"builtins": {"task"}}, "alias 'task' conflicts with a built-in command"),
            ("d", {"existing": {"d": "deploy"}}, "alias 'd' is already registered for 'deploy'"),
        ],
    )
    def test_texts(self, name, kw, text):
        with pytest.raises(AliasNameError, match=text) as exc:
            validate_alias_name(name, **kw)
        assert isinstance(exc.value, ValueError)

    def test_ok(self):
        validate_alias_name("tl", builtins=["task"], existing={"d": "deploy"})


class TestBridgeOptions:
    def _cli(self):
        @click.group()
        def cli():
            pass

        @cli.command(help="Deploy the app")
        def deploy():
            click.echo("ran deploy")

        return cli

    def test_mapping_source_keeps_native_default(self):
        cli = self._cli()
        bridge_to_click(cli, {"d": "deploy"})
        assert cli.get_command(click.Context(cli), "d").name == "deploy"

    def test_options_resolve_to_a_proxy(self):
        cli = self._cli()
        bridge_to_click(cli, {"d": "deploy"}, hidden=False, describe=lambda n, t: f"{n} -> {t}")
        cmd = cli.get_command(click.Context(cli), "d")
        assert cmd.name == "d"
        assert cmd.hidden is False
        assert cmd.help == "d -> deploy"
        res = CliRunner().invoke(cli, ["d"])
        assert res.exit_code == 0, res.output
        assert "ran deploy" in res.output

    def test_register_only_runs_nothing(self):
        cli = self._cli()
        bridge_to_click(cli, {"d": "deploy"}, dispatch=False)
        res = CliRunner().invoke(cli, ["d", "--anything"])
        assert res.exit_code == 0, res.output
        assert res.output == ""
