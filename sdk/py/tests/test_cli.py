import re

import typer
from typer.testing import CliRunner

from hop_top_kit.cli import (
    DARK,
    NEON,
    Disable,
    GlobalFlag,
    GroupConfig,
    HelpConfig,
    Palette,
    Theme,
    _build_theme,
    _make_rich_help_config,
    channel,
    create_app,
    register_stream,
    set_command_group,
    verbose_count,
)

runner = CliRunner()


# ---------------------------------------------------------------------------
# Palette / Theme constants
# ---------------------------------------------------------------------------


def test_neon_palette_values():
    assert NEON.command == "#7ED957"
    assert NEON.flag == "#FF00FF"


def test_dark_palette_values():
    assert DARK.command == "#C1FF72"
    assert DARK.flag == "#FF66C4"


# ---------------------------------------------------------------------------
# _build_theme
# ---------------------------------------------------------------------------


def test_build_theme_defaults_to_neon():
    t = _build_theme()
    assert t.palette.command == NEON.command
    assert t.palette.flag == NEON.flag
    assert t.accent == NEON.command
    assert t.secondary == NEON.flag


def test_build_theme_accent_overrides_command():
    t = _build_theme(accent="#AABBCC")
    assert t.palette.command == "#AABBCC"
    assert t.accent == "#AABBCC"
    # flag unchanged from NEON
    assert t.palette.flag == NEON.flag


def test_build_theme_semantic_colors():
    t = _build_theme()
    assert t.muted == "#858183"
    assert t.error == "#ED4A5E"
    assert t.success == "#52CF84"


def test_build_theme_returns_theme_instance():
    t = _build_theme()
    assert isinstance(t, Theme)
    assert isinstance(t.palette, Palette)


# ---------------------------------------------------------------------------
# create_app return type
# ---------------------------------------------------------------------------


def test_create_app_returns_tuple():
    result = create_app(name="mytool", version="1.2.3", help="A tool")
    assert isinstance(result, tuple)
    assert len(result) == 2


def test_create_app_first_element_is_typer():
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    assert isinstance(app, typer.Typer)


def test_create_app_second_element_is_theme():
    _, theme = create_app(name="mytool", version="1.2.3", help="A tool")
    assert isinstance(theme, Theme)


def test_create_app_default_theme_is_neon():
    _, theme = create_app(name="mytool", version="1.2.3", help="A tool")
    assert theme.palette.command == NEON.command
    assert theme.palette.flag == NEON.flag


def test_create_app_accent_propagates_to_theme():
    _, theme = create_app(name="mytool", version="1.2.3", help="A tool", accent="#DEADBE")
    assert theme.palette.command == "#DEADBE"
    assert theme.accent == "#DEADBE"


# ---------------------------------------------------------------------------
# Existing behaviour unchanged
# ---------------------------------------------------------------------------


def test_version_flag():
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    result = runner.invoke(app, ["--version"])
    assert result.exit_code == 0
    assert "mytool v1.2.3" in result.output


def test_no_help_subcommand():
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    from typer.main import get_command

    cmd = get_command(app)
    subcommand_names = list(cmd.commands.keys()) if hasattr(cmd, "commands") else []
    assert "help" not in subcommand_names
    assert "completion" not in subcommand_names


def test_format_default():
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")

    @app.command()
    def run(format: str = "table"):
        print(format)

    result = runner.invoke(app, ["run"])
    assert "table" in result.output


# ---------------------------------------------------------------------------
# Help theming — ANSI color injection
# ---------------------------------------------------------------------------

_ANSI_RE = re.compile(r"\x1b\[[^m]*m")


def _has_ansi(text: str) -> bool:
    return bool(_ANSI_RE.search(text))


def test_help_contains_ansi_codes():
    """--help output should include ANSI escape codes when color is enabled."""
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    result = runner.invoke(app, ["--help"], color=True)
    assert result.exit_code == 0
    assert _has_ansi(result.output), "Expected ANSI codes in help output"


def test_help_no_color_env_suppresses_ansi(monkeypatch):
    """NO_COLOR=1 env var must suppress all ANSI codes in help output."""
    monkeypatch.setenv("NO_COLOR", "1")
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    result = runner.invoke(app, ["--help"], color=True)
    assert result.exit_code == 0
    assert not _has_ansi(result.output), "Expected no ANSI codes when NO_COLOR=1"


def test_help_no_color_param_suppresses_ansi():
    """no_color=True param must suppress all ANSI codes in help output."""
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool", no_color=True)
    result = runner.invoke(app, ["--help"], color=True)
    assert result.exit_code == 0
    assert not _has_ansi(result.output), "Expected no ANSI codes when no_color=True"


def test_help_heading_white_color():
    """Headings must use white (#FFFFFF → \\x1b[38;2;255;255;255m)."""
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    result = runner.invoke(app, ["--help"], color=True)
    assert result.exit_code == 0
    assert "\x1b[38;2;255;255;255m" in result.output, "Expected white ANSI code for heading"


def test_help_option_neon_flag_color():
    """Option names must use Neon.Flag (#FF00FF → \\x1b[38;2;255;0;255m)."""
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")
    result = runner.invoke(app, ["--help"], color=True)
    assert result.exit_code == 0
    assert "\x1b[38;2;255;0;255m" in result.output, (
        "Expected neon-flag ANSI code (#FF00FF) for option names"
    )


def test_make_rich_help_config_no_color_still_has_context():
    """_make_rich_help_config returns context_class even when no_color=True
    (needed for section header renaming)."""
    theme = _build_theme()
    cfg = _make_rich_help_config(theme, no_color=True)
    assert "context_class" in cfg
    import click

    assert issubclass(cfg["context_class"], click.Context)


def test_make_rich_help_config_no_color_env_still_has_context(monkeypatch):
    """_make_rich_help_config returns context_class even when NO_COLOR is set."""
    monkeypatch.setenv("NO_COLOR", "1")
    theme = _build_theme()
    cfg = _make_rich_help_config(theme, no_color=False)
    assert "context_class" in cfg


def test_make_rich_help_config_color_has_context_class():
    """_make_rich_help_config returns context_class key when color is on."""
    theme = _build_theme()
    cfg = _make_rich_help_config(theme, no_color=False)
    assert "context_class" in cfg
    import click

    assert issubclass(cfg["context_class"], click.Context)


# ---------------------------------------------------------------------------
# Disable dataclass
# ---------------------------------------------------------------------------


def test_disable_format_removes_format_from_help():
    """Disable(format=True) → --format not present in root help output."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        disable=Disable(format=True),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--format" not in result.output


def test_disable_quiet_removes_quiet_from_help():
    """Disable(quiet=True) → --quiet not present in root help output."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        disable=Disable(quiet=True),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--quiet" not in result.output


def test_disable_no_color_removes_no_color_from_help():
    """Disable(no_color=True) → --no-color not present in root help output."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        disable=Disable(no_color=True),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--no-color" not in result.output


def test_help_config_show_aliases_accepted():
    """HelpConfig(show_aliases=True) is accepted without error."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        help_config=HelpConfig(show_aliases=True),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0


def test_disable_format_does_not_suppress_hints():
    """Disable(format=True) → --no-hints still present."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        disable=Disable(format=True),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--format" not in result.output
    assert "--no-hints" in result.output, "--no-hints must survive when only format is disabled"


def test_disable_zero_value_keeps_all_flags():
    """Disable() (all False) → built-in flags still present."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        disable=Disable(),
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--format" in result.output
    assert "--quiet" in result.output
    assert "--no-color" in result.output


# ---------------------------------------------------------------------------
# GlobalFlag
# ---------------------------------------------------------------------------


def test_global_flag_appears_in_help():
    """GlobalFlag(name='verbose') → --verbose present in root help output."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        globals=[GlobalFlag(name="verbose", usage="set log level", default="info")],
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--verbose" in result.output


def test_global_flag_usage_in_help():
    """GlobalFlag usage string appears in help."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        globals=[GlobalFlag(name="verbose", usage="set log level", default="info")],
    )
    result = runner.invoke(app, ["--help"])
    assert "set log level" in result.output


def test_global_flag_default_value():
    """GlobalFlag default propagates to option default."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        globals=[GlobalFlag(name="verbose", usage="set level", default="info")],
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    # default 'info' should appear somewhere in help (Click shows defaults)
    assert "info" in result.output


def test_global_flag_short_form():
    """GlobalFlag with short → short flag appears in help."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        globals=[GlobalFlag(name="output", usage="output path", short="o")],
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--output" in result.output
    assert "-o" in result.output


def test_multiple_global_flags():
    """Multiple GlobalFlags all appear in help."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        globals=[
            GlobalFlag(name="profile", usage="config profile"),
            GlobalFlag(name="region", usage="cloud region", default="us-east-1"),
        ],
    )
    result = runner.invoke(app, ["--help"])
    assert result.exit_code == 0
    assert "--profile" in result.output
    assert "--region" in result.output


# ---------------------------------------------------------------------------
# Help structure — line-by-line parity
# ---------------------------------------------------------------------------

_ANSI_RE = re.compile(r"\x1b\[[^m]*m")


def _strip(s: str) -> str:
    return _ANSI_RE.sub("", s)


def _help_lines() -> list[str]:
    """Return plain (ANSI-stripped) help lines for a tool with one subcommand."""
    app, _ = create_app(name="mytool", version="1.2.3", help="A tool")

    @app.command()
    def sub():
        """A subcommand"""

    result = runner.invoke(app, ["--help"])
    return _strip(result.output).split("\n")


def test_help_first_nonempty_line_is_usage():
    """First non-empty line starts with 'Usage:'."""
    lines = _help_lines()
    first = next((l for l in lines if l.strip()), "")
    assert first.startswith("Usage:"), f"got: {first!r}"


def test_help_description_before_sections():
    """Description appears before the first section header."""
    lines = _help_lines()
    desc_idx = next((i for i, l in enumerate(lines) if "A tool" in l), -1)
    sec_idx = next(
        (i for i, l in enumerate(lines) if l.strip() in ("COMMANDS:", "FLAGS:")),
        -1,
    )
    assert desc_idx >= 0, "description not found"
    assert sec_idx >= 0, "section header not found"
    assert desc_idx < sec_idx, "description must appear before first section"


def test_help_commands_before_flags():
    """COMMANDS: section appears before FLAGS: section."""
    lines = _help_lines()
    cmd_idx = next((i for i, l in enumerate(lines) if l.strip() == "COMMANDS:"), -1)
    flag_idx = next((i for i, l in enumerate(lines) if l.strip() == "FLAGS:"), -1)
    assert cmd_idx >= 0, "COMMANDS: not found"
    assert flag_idx >= 0, "FLAGS: not found"
    assert cmd_idx < flag_idx, "COMMANDS must come before FLAGS"


def test_help_sub_command_under_commands():
    """'sub' command entry appears after COMMANDS: header."""
    lines = _help_lines()
    cmd_idx = next((i for i, l in enumerate(lines) if l.strip() == "COMMANDS:"), -1)
    sub_idx = next((i for i, l in enumerate(lines) if re.match(r"^\s+sub\b", l)), -1)
    assert sub_idx > cmd_idx, "sub must appear after COMMANDS:"


def test_help_flags_under_flags_section():
    """--format, --quiet, --no-color, --no-hints all appear after FLAGS: header."""
    lines = _help_lines()
    flag_idx = next((i for i, l in enumerate(lines) if l.strip() == "FLAGS:"), -1)
    assert flag_idx >= 0, "FLAGS: section not found"
    flag_lines = lines[flag_idx + 1 :]
    for flag in ("--format", "--quiet", "--no-color", "--no-hints"):
        assert any(flag in l for l in flag_lines), f"{flag} not found under FLAGS:"


def test_help_no_help_or_completion_as_command():
    """Neither 'help' nor 'completion' appears as a command entry."""
    lines = _help_lines()
    cmd_idx = next((i for i, l in enumerate(lines) if l.strip() == "COMMANDS:"), -1)
    flag_idx = next((i for i, l in enumerate(lines) if l.strip() == "FLAGS:"), -1)
    cmd_lines = lines[cmd_idx + 1 : flag_idx]
    assert not any(re.match(r"^\s+help\b", l) for l in cmd_lines), (
        "'help' must not appear as a subcommand"
    )
    assert not any(re.match(r"^\s+completion\b", l) for l in cmd_lines), (
        "'completion' must not appear as a subcommand"
    )


def test_help_section_headers_are_uppercase():
    """Section headers use parity titles COMMANDS and FLAGS, not Commands/Options."""
    lines = _help_lines()
    assert not any(l.strip() == "Commands:" for l in lines), (
        "Commands: must be renamed to COMMANDS:"
    )
    assert not any(l.strip() == "Options:" for l in lines), "Options: must be renamed to FLAGS:"
    assert any(l.strip() == "COMMANDS:" for l in lines), "COMMANDS: header missing"
    assert any(l.strip() == "FLAGS:" for l in lines), "FLAGS: header missing"


def test_help_version_flag_present():
    """--version flag (as -v/--version) appears in the FLAGS section."""
    lines = _help_lines()
    flag_idx = next((i for i, l in enumerate(lines) if l.strip() == "FLAGS:"), -1)
    flag_lines = lines[flag_idx + 1 :]
    assert any("--version" in l for l in flag_lines), "--version not found in FLAGS:"


# ---------------------------------------------------------------------------
# Command groups — COMMANDS vs MANAGEMENT with --help-all
# ---------------------------------------------------------------------------


def _grouped_app():
    """Build app with grouped commands for group tests."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        help_config=HelpConfig(
            groups=[
                GroupConfig(id="commands", title="COMMANDS"),
                GroupConfig(id="management", title="MANAGEMENT", hidden=True),
            ],
        ),
    )

    @app.command()
    def run():
        """Run something"""

    @app.command()
    def status():
        """Show status"""

    @app.command()
    def config():
        """Manage configuration"""

    @app.command()
    def toolspec():
        """Load toolspec"""

    set_command_group("config", "management")
    set_command_group("toolspec", "management")
    return app


def test_help_groups_default_hides_management():
    """--help hides commands in hidden groups (MANAGEMENT)."""
    app = _grouped_app()
    result = runner.invoke(app, ["--help"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "COMMANDS:" in plain
    assert "MANAGEMENT:" not in plain
    assert "run" in plain
    assert "status" in plain
    assert "config" not in plain
    assert "toolspec" not in plain


def test_help_groups_help_all_shows_all():
    """--help-all shows all groups including hidden ones."""
    app = _grouped_app()
    result = runner.invoke(app, ["--help-all"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "COMMANDS:" in plain
    assert "MANAGEMENT:" in plain
    assert "run" in plain
    assert "status" in plain
    assert "config" in plain
    assert "toolspec" in plain


# ---------------------------------------------------------------------------
# Per-group help — --help-<id> flag and help <id> subcommand
# ---------------------------------------------------------------------------


def _grouped_app_with_extras():
    """Build app with 3 groups (one non-hidden) for per-group help tests."""
    app, _ = create_app(
        name="mytool",
        version="1.0.0",
        help="A tool",
        help_config=HelpConfig(
            groups=[
                GroupConfig(id="commands", title="COMMANDS"),
                GroupConfig(id="management", title="MANAGEMENT", hidden=True),
                GroupConfig(id="extras", title="EXTRAS"),
            ],
        ),
    )

    @app.command()
    def run():
        """Run something"""

    @app.command()
    def status():
        """Show status"""

    @app.command()
    def config():
        """Manage configuration"""

    @app.command()
    def toolspec():
        """Load toolspec"""

    @app.command()
    def bonus():
        """Bonus feature"""

    set_command_group("config", "management")
    set_command_group("toolspec", "management")
    set_command_group("bonus", "extras")
    return app


def test_help_per_group_flag():
    """--help-management shows only management group commands + FLAGS."""
    app = _grouped_app_with_extras()
    result = runner.invoke(app, ["--help-management"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "MANAGEMENT:" in plain
    assert "config" in plain
    assert "toolspec" in plain
    # Other groups must NOT appear
    assert "COMMANDS:" not in plain
    assert "EXTRAS:" not in plain
    assert "run" not in plain
    assert "status" not in plain
    assert "bonus" not in plain
    # FLAGS section still shown
    assert "FLAGS:" in plain


def test_help_per_group_subcommand():
    """help management shows only management group commands."""
    app = _grouped_app_with_extras()
    result = runner.invoke(app, ["help", "management"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "MANAGEMENT:" in plain
    assert "config" in plain
    assert "toolspec" in plain
    assert "COMMANDS:" not in plain
    assert "EXTRAS:" not in plain
    assert "run" not in plain
    assert "bonus" not in plain
    assert "FLAGS:" in plain


def test_help_per_group_all():
    """help all shows everything (same as --help-all)."""
    app = _grouped_app_with_extras()
    result = runner.invoke(app, ["help", "all"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "COMMANDS:" in plain
    assert "MANAGEMENT:" in plain
    assert "EXTRAS:" in plain
    assert "run" in plain
    assert "config" in plain
    assert "bonus" in plain


# ---------------------------------------------------------------------------
# Shell completion — management group
# ---------------------------------------------------------------------------


def test_completion_command_exists():
    """completion command registered on grouped apps."""
    app = _grouped_app()
    from typer.main import get_command

    cmd = get_command(app)
    assert "completion" in cmd.commands


def test_completion_hidden_from_default_help():
    """completion must NOT appear in default --help (hidden management)."""
    app = _grouped_app()
    result = runner.invoke(app, ["--help"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "completion" not in plain


def test_completion_visible_in_help_all():
    """completion must appear in --help-all output."""
    app = _grouped_app()
    result = runner.invoke(app, ["--help-all"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "completion" in plain


def test_completion_visible_in_help_management():
    """completion must appear in --help-management output."""
    app = _grouped_app()
    result = runner.invoke(app, ["--help-management"])
    plain = _strip(result.output)
    assert result.exit_code == 0
    assert "completion" in plain


def test_completion_show_bash():
    """completion show bash outputs a completion script."""
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "show", "bash"])
    assert result.exit_code == 0
    assert "complete" in result.output
    assert "COMP_WORDS" in result.output


def test_completion_show_zsh():
    """completion show zsh outputs a zsh completion script."""
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "show", "zsh"])
    assert result.exit_code == 0
    assert "compdef" in result.output


def test_completion_show_fish():
    """completion show fish outputs a fish completion script."""
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "show", "fish"])
    assert result.exit_code == 0
    assert "commandline" in result.output


def test_completion_show_invalid_shell():
    """completion show <invalid> exits non-zero."""
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "show", "nushell"])
    assert result.exit_code != 0


def test_completion_install_bash(tmp_path, monkeypatch):
    """completion install bash writes script + sources from rc."""
    monkeypatch.setattr("pathlib.Path.home", lambda: tmp_path)
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "install", "bash"])
    assert result.exit_code == 0
    assert "installed" in result.output.lower() or "bash" in result.output.lower()
    # Verify file was created
    completion_path = tmp_path / ".bash_completions" / "mytool.sh"
    assert completion_path.exists()


def test_completion_install_zsh(tmp_path, monkeypatch):
    """completion install zsh writes script to ~/.zfunc/."""
    monkeypatch.setattr("pathlib.Path.home", lambda: tmp_path)
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "install", "zsh"])
    assert result.exit_code == 0
    completion_path = tmp_path / ".zfunc" / "_mytool"
    assert completion_path.exists()


def test_completion_install_fish(tmp_path, monkeypatch):
    """completion install fish writes script to fish completions dir."""
    monkeypatch.setattr("pathlib.Path.home", lambda: tmp_path)
    app = _grouped_app()
    result = runner.invoke(app, ["completion", "install", "fish"])
    assert result.exit_code == 0
    completion_path = tmp_path / ".config" / "fish" / "completions" / "mytool.fish"
    assert completion_path.exists()


# ---------------------------------------------------------------------------
# Verbose flag (-V / --verbose)
# ---------------------------------------------------------------------------


def _verbose_app():
    """App with a command that echoes verbose count."""
    app, _ = create_app(name="vtool", version="0.1.0", help="Verbose test")

    @app.command()
    def info():
        """Show verbose level."""
        typer.echo(f"v={verbose_count()}")

    return app


def test_verbose_default_zero():
    app = _verbose_app()
    result = runner.invoke(app, ["info"])
    assert result.exit_code == 0
    assert "v=0" in result.output


def test_verbose_single_v():
    app = _verbose_app()
    result = runner.invoke(app, ["-V", "info"])
    assert result.exit_code == 0
    assert "v=1" in result.output


def test_verbose_double_vv():
    app = _verbose_app()
    result = runner.invoke(app, ["-V", "-V", "info"])
    assert result.exit_code == 0
    assert "v=2" in result.output


def test_verbose_quiet_overrides():
    """--quiet forces verbose count to 0."""
    app = _verbose_app()
    result = runner.invoke(app, ["-V", "--quiet", "info"])
    assert result.exit_code == 0
    assert "v=0" in result.output


def test_verbose_flag_in_help():
    app, _ = create_app(name="vtool", version="0.1.0", help="A tool")
    result = runner.invoke(app, ["--help"])
    assert "-V" in result.output
    assert "--verbose" in result.output


# ---------------------------------------------------------------------------
# Named streams — register_stream / channel
# ---------------------------------------------------------------------------


def test_stream_channel_disabled_no_output(capsys):
    """Disabled stream channel produces no stderr output."""
    from hop_top_kit.cli import _get_enabled_streams

    es = _get_enabled_streams()
    es.pop("mycmd", None)
    register_stream("mycmd", "audit", "Audit trail")
    w = channel("mycmd", "audit")
    w.write("should not appear\n")
    captured = capsys.readouterr()
    assert captured.err == ""


def test_stream_channel_enabled_prefixes(capsys):
    """Enabled stream channel writes [name] prefix to stderr."""
    from hop_top_kit.cli import _get_enabled_streams

    register_stream("mycmd2", "trace", "Trace output")
    es = _get_enabled_streams()
    es["mycmd2"] = {"trace"}
    w = channel("mycmd2", "trace")
    w.write("hello\n")
    captured = capsys.readouterr()
    assert "[trace] hello\n" in captured.err
    es.pop("mycmd2", None)


def test_stream_register_idempotent():
    """Multiple register_stream calls accumulate defs."""
    from hop_top_kit.cli import _get_stream_registry

    reg = _get_stream_registry()
    reg.pop("rcmd", None)
    register_stream("rcmd", "a", "A stream")
    register_stream("rcmd", "b", "B stream")
    assert len(reg["rcmd"]) == 2
    reg.pop("rcmd", None)
