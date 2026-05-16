"""
hop_top_kit.cli — Typer app factory implementing the hop-top CLI contract.

Requirements:
    Python >=3.11
    typer  >=0.12

This module exposes the following public symbols:

    create_app        — factory returning (Typer, Theme)
    Palette           — dataclass holding command + flag hex colors
    Theme             — dataclass holding semantic palette colors
    Disable           — opt-out built-in global flags
    GlobalFlag        — extra persistent flag on root command
    GroupConfig       — command group definition (id, title, hidden)
    set_command_group — tag a command to a group
    register_stream   — register a named output stream on a command
    channel           — get a writer for a named stream
    verbose_count     — get the current -V count from context
    NEON              — built-in vivid palette (#7ED957, #FF00FF)
    DARK              — built-in softer palette (#C1FF72, #FF66C4)

Usage:
    from hop_top_kit.cli import create_app
    app, theme = create_app(name="mytool", version="1.2.3", help="Does things")
"""

from __future__ import annotations

import contextvars
import inspect
import io
import json
import os
import pathlib
import re
import sys
import threading
from dataclasses import dataclass, field
from typing import Optional, TextIO

import click
import typer

_PARITY_PATH = pathlib.Path(__file__).parents[3] / "contracts" / "parity" / "parity.json"
_PARITY: dict = json.loads(_PARITY_PATH.read_text())


@dataclass
class Disable:
    """Opt-out built-in global flags. Zero value = all enabled."""

    format: bool = False
    quiet: bool = False
    no_color: bool = False
    hints: bool = False  # suppress --no-hints flag


@dataclass
class GroupConfig:
    """Command group definition for --help rendering."""

    id: str
    title: str
    hidden: bool = False


@dataclass
class HelpConfig:
    """Root --help layout overrides. Zero value uses parity.json defaults."""

    disclaimer: str = ""
    """Appended to the help text as a separate paragraph when non-empty."""
    section_order: list[str] = field(default_factory=list)
    """Section rendering order (e.g. ['commands', 'options']).
    Empty = use parity.json default."""
    show_aliases: bool = False
    """Show command aliases in help output. Default hidden."""
    groups: list[GroupConfig] = field(default_factory=list)
    """Command groups for partitioned help output."""


# Module-level registry: command name → group ID.
_command_groups: dict[str, str] = {}


def set_command_group(name: str, group_id: str) -> None:
    """Tag a command to render under a specific group in --help."""
    _command_groups[name] = group_id


# ---------------------------------------------------------------------------
# Verbose count accessor (contextvars for request-scoped safety)
# ---------------------------------------------------------------------------

_verbose_count: contextvars.ContextVar[int] = contextvars.ContextVar(
    "verbose_count",
    default=0,
)
_quiet_flag: contextvars.ContextVar[bool] = contextvars.ContextVar(
    "quiet_flag",
    default=False,
)


def verbose_count() -> int:
    """Return the current -V count. 0=info, 1=debug, 2+=trace."""
    return _verbose_count.get()


# ---------------------------------------------------------------------------
# Named streams — register_stream / channel
# ---------------------------------------------------------------------------

# command name → list of stream defs
_stream_registry: contextvars.ContextVar[dict[str, list[dict[str, str]]]] = contextvars.ContextVar(
    "stream_registry", default=None
)
# command name → set of enabled stream names
_enabled_streams: contextvars.ContextVar[dict[str, set[str]]] = contextvars.ContextVar(
    "enabled_streams", default=None
)


def _get_stream_registry() -> dict[str, list[dict[str, str]]]:
    reg = _stream_registry.get()
    if reg is None:
        reg = {}
        _stream_registry.set(reg)
    return reg


def _get_enabled_streams() -> dict[str, set[str]]:
    es = _enabled_streams.get()
    if es is None:
        es = {}
        _enabled_streams.set(es)
    return es


class _NullWriter(io.TextIOBase):
    """No-op writer discarding all output."""

    def write(self, s: str) -> int:
        return len(s)


class _StreamChannel(io.TextIOBase):
    """Thread-safe writer prepending [name] prefix to each line on stderr."""

    def __init__(self, name: str) -> None:
        self._prefix = f"[{name}] "
        self._lock = threading.Lock()

    def write(self, s: str) -> int:
        with self._lock:
            for line in s.splitlines(keepends=True):
                if line:
                    sys.stderr.write(self._prefix + line)
        return len(s)


_null_writer = _NullWriter()


def register_stream(cmd_name: str, name: str, description: str) -> None:
    """Register a named stream on a command (by command name).

    Parity note: Go uses StringSlice (repeatable --stream); Python and TS
    use a single comma-separated string value.
    """
    reg = _get_stream_registry()
    defs = reg.setdefault(cmd_name, [])
    defs.append({"name": name, "description": description})


def channel(cmd_name: str, name: str) -> TextIO:
    """Return a writer for the named stream.

    If --stream includes *name*, writes to stderr with ``[name] `` prefix.
    Otherwise returns a no-op writer.
    """
    enabled = _get_enabled_streams().get(cmd_name, set())
    if name not in enabled:
        return _null_writer  # type: ignore[return-value]
    return _StreamChannel(name)  # type: ignore[return-value]


@dataclass
class GlobalFlag:
    """Extra tool-specific persistent flag on root command."""

    name: str
    usage: str
    short: str = ""
    default: str = ""


@dataclass
class Palette:
    """Brand colors used across a theme.

    Fields mirror Go's ``cli.Palette`` struct (hex strings instead of
    ``color.Color`` values since Python has no equivalent stdlib type).
    """

    command: str  # commands / primary accent
    flag: str  # flags / secondary accent


@dataclass
class Theme:
    """Semantic colors for CLI output.

    Mirrors Go's ``cli.Theme`` struct.  Pre-built styles (lipgloss) are
    omitted — Python equivalents live in the Rich / Textual layer.
    """

    palette: Palette
    accent: str
    secondary: str
    muted: str
    error: str
    success: str


# Built-in palettes — exact hex values from Go's cli.Palette constants.
NEON = Palette(command="#7ED957", flag="#FF00FF")
DARK = Palette(command="#C1FF72", flag="#FF66C4")


def _build_theme(accent: str = "") -> Theme:
    """Build a Theme from an optional accent override.

    Logic mirrors Go's ``buildTheme``:
    - Default palette: NEON.
    - If *accent* is non-empty, ``palette.command`` is overridden with it.
    - Semantic colors: muted=#858183, error=#ED4A5E, success=#52CF84.
    """
    p = Palette(command=NEON.command, flag=NEON.flag)
    if accent:
        p.command = accent
    return Theme(
        palette=p,
        accent=p.command,
        secondary=p.flag,
        muted="#858183",
        error="#ED4A5E",
        success="#52CF84",
    )


def _ansi_rgb(hex_color: str) -> str:
    """Return an ANSI 24-bit foreground SGR prefix for *hex_color* (#RRGGBB)."""
    h = hex_color.lstrip("#")
    r, g, b = int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16)
    return f"\x1b[38;2;{r};{g};{b}m"


_ANSI_RESET = "\x1b[0m"
_ANSI_BOLD = "\x1b[1m"

# Fixed brand color constants (match Go's fang ColorScheme values).
_WHITE = "#FFFFFF"
_ARG_COLOR = "#B5E89B"


class _BrandHelpFormatter(click.HelpFormatter):
    """Click HelpFormatter that applies hop-top brand colors to help output.

    Color rules (mirrors Go's fang ColorScheme):
      Headings (Options, Commands, Arguments): white (#FFFFFF) + bold
      Usage program name:                      theme.accent  (#7ED957)
      Option names (--flag):                   theme.secondary (#FF00FF)
      Command names:                           theme.accent  (#7ED957)
      Argument placeholders:                   #B5E89B
    """

    _theme: Theme
    _no_color: bool = False

    # Click→fang section name mapping (lowercase click heading → fang key).
    _CLICK_TO_FANG: dict[str, str] = {
        "options": "flags",
        "commands": "commands",
        "arguments": "arguments",
        "aliases": "aliases",
        "examples": "examples",
    }

    # Parity section title lookup built at class definition time.
    _PARITY_SECTION_TITLES: dict[str, str] = {
        fang_key: cfg["title"]
        for fang_key, cfg in _PARITY.get("help", {}).get("sections", {}).items()
    }

    def __init__(self, theme: Theme | None = None, *args, **kwargs) -> None:
        super().__init__(*args, **kwargs)
        if theme is not None:
            self._theme = theme

    # ------------------------------------------------------------------
    # Heading (e.g. "Options:", "Commands:", "Arguments:")
    # ------------------------------------------------------------------

    def write_heading(self, heading: str) -> None:
        fang_key = self._CLICK_TO_FANG.get(heading.lower())
        display = self._PARITY_SECTION_TITLES.get(fang_key or "", heading) if fang_key else heading
        if self._no_color:
            self.write(f"{'':>{self.current_indent}}{display}:\n")
        else:
            prefix = _ansi_rgb(_WHITE) + _ANSI_BOLD
            self.write(f"{'':>{self.current_indent}}{prefix}{display}:{_ANSI_RESET}\n")

    # ------------------------------------------------------------------
    # Usage line
    # ------------------------------------------------------------------

    def write_usage(self, prog: str, args: str = "", prefix: str | None = None) -> None:
        if prefix is None:
            prefix = "Usage: "
        if self._no_color:
            self.write(f"{prefix}{prog} {args}\n")
        else:
            colored_prog = _ansi_rgb(self._theme.accent) + prog + _ANSI_RESET
            self.write(f"{prefix}{colored_prog} {args}\n")

    # ------------------------------------------------------------------
    # Definition list (flags + commands)
    # ------------------------------------------------------------------

    def write_dl(
        self,
        rows: list[tuple[str, str]],
        col_max: int = 30,
        col_spacing: int = 2,
    ) -> None:
        colored_rows: list[tuple[str, str]] = []
        for first, second in rows:
            colored_first = self._color_dl_term(first)
            colored_rows.append((colored_first, second))
        # Delegate to parent to handle wrapping / alignment — but we need to
        # write the colored terms ourselves since parent uses plain strings.
        # Use parent's logic by temporarily patching; simpler: just write directly.
        self._write_dl_direct(colored_rows, col_max, col_spacing)

    def _color_dl_term(self, term: str) -> str:
        """Apply brand color to a definition-list term (flag or command name)."""
        if self._no_color:
            return term
        stripped = term.strip()
        if stripped.startswith("-"):
            parts = term.split(",")
            colored_parts = []
            for p in parts:
                colored_parts.append(_ansi_rgb(self._theme.secondary) + p.strip() + _ANSI_RESET)
            return ", ".join(colored_parts)
        return _ansi_rgb(self._theme.accent) + term + _ANSI_RESET

    def _write_dl_direct(
        self,
        rows: list[tuple[str, str]],
        col_max: int,
        col_spacing: int,
    ) -> None:
        """Write colored definition list rows with proper indentation."""
        # Measure visible (non-ANSI) widths for alignment.
        ansi_re = re.compile(r"\x1b\[[^m]*m")

        def visible_len(s: str) -> int:
            return len(ansi_re.sub("", s))

        current = self.current_indent
        first_col_width = min(
            max((visible_len(r[0]) for r in rows), default=0),
            col_max,
        )
        indent = " " * current
        for first, second in rows:
            vlen = visible_len(first)
            if vlen <= first_col_width:
                padding = " " * (first_col_width - vlen + col_spacing)
                self.write(f"{indent}{first}{padding}{second}\n")
            else:
                # Term too long: put description on next line.
                self.write(f"{indent}{first}\n")
                padding = " " * (current + first_col_width + col_spacing)
                self.write(f"{padding}{second}\n")


def _make_rich_help_config(theme: Theme, no_color: bool) -> dict:
    """Build a config dict for brand-colored help output.

    Returns a dict with a ``"context_class"`` key pointing to a
    ``click.Context`` subclass whose ``formatter_class`` is set to
    ``_BrandHelpFormatter`` (pre-bound to *theme*).

    When *no_color* is True (or the NO_COLOR env var is set), returns an
    empty dict so Click uses its plain default formatter.

    Note: ``formatter_class`` is a class attribute on ``click.Context``,
    not a ``Context.__init__()`` kwarg, so it cannot go in
    ``context_settings``.  The caller must consume the ``"context_class"``
    key and wire it onto the Click command / TyperGroup subclass.
    """
    is_no_color = no_color or bool(os.environ.get("NO_COLOR"))

    # Bind theme + no_color flag into a dedicated formatter class via type().
    bound_formatter = type(
        "_BoundBrandHelpFormatter",
        (_BrandHelpFormatter,),
        {"_theme": theme, "_no_color": is_no_color},
    )

    # Create a Context subclass whose formatter_class uses the brand formatter.
    brand_context = type(
        "_BrandContext",
        (click.Context,),
        {"formatter_class": bound_formatter},
    )

    return {"context_class": brand_context}


def create_app(
    *,
    name: str,
    version: str,
    help: str,
    accent: str = "",
    no_color: bool = False,
    disable: Disable | None = None,
    globals: list[GlobalFlag] | None = None,
    help_config: HelpConfig | None = None,
) -> tuple[typer.Typer, Theme]:
    """Create a Typer app pre-configured to the hop-top CLI contract.

    All parameters are keyword-only.

    Args:
        name:    Binary name shown in help text and in the ``-v`` version line.
                 Must be a non-empty string (e.g. ``"mytool"``).
        version: SemVer string printed by ``-v`` / ``--version``
                 (e.g. ``"1.2.3"``).  Not validated; any string is accepted.
        help:    One-line description shown at the top of ``--help`` output.
        accent:   Optional hex color string (e.g. ``"#FF0000"``) used as the
                  theme accent / command color.  Defaults to the NEON palette
                  command color when empty.
        no_color: Deprecated shorthand; same as ``Disable(no_color=True)``.
                  The NO_COLOR env var has the same effect.
        disable:  ``Disable`` instance to selectively suppress built-in flags
                  (format, quiet, no_color).  Merged with *no_color* param.
        globals:  Extra ``GlobalFlag`` entries to add to the root callback.

    Returns:
        A tuple of ``(typer.Typer, Theme)`` where the Typer instance is
        configured as follows:

        - ``add_completion=False`` — suppresses ``--install-completion`` and
          ``--show-completion`` flags.  hop-top tools ship shell completions
          via a separate mechanism; injecting them through Typer would expose
          implementation details and pollute ``--help``.
        - ``no_args_is_help=True`` — when the binary is invoked with no
          arguments and no registered subcommand matches, Typer prints the
          help page and exits 0.  Combined with the ``invoke_without_command=True``
          callback below, the callback still runs on every invocation but only
          echoes help if ``ctx.invoked_subcommand is None``; the two settings
          are complementary, not redundant.
        - Root callback registered with ``invoke_without_command=True`` so the
          ``-v`` / ``--version`` eager flag is evaluated before any subcommand
          dispatch.  ``is_eager=True`` causes Typer/Click to process the flag
          immediately and raise ``Exit``, bypassing any pending subcommand.
        - Version output format: ``"<name> v<version>"`` on a single line,
          followed by process exit 0.

    Example:
        from hop_top_kit.cli import create_app
        import typer

        app, theme = create_app(name="mytool", version="1.2.3", help="Does things")

        @app.command()
        def run(format: str = "table") -> None:
            typer.echo(format)

        # Produces: "mytool v1.2.3"
        # runner.invoke(app, ["-v"])

        # Produces help page and exits 0
        # runner.invoke(app, [])
    """
    import typer.core

    # Merge no_color (legacy param) into disable.
    dis = disable or Disable()
    if no_color:
        dis = Disable(
            format=dis.format,
            quiet=dis.quiet,
            no_color=True,
        )

    hcfg = help_config or HelpConfig()
    theme = _build_theme(accent)
    rich_help_cfg = _make_rich_help_config(theme, dis.no_color)

    # Wire brand context class (if color enabled) onto a TyperGroup subclass.
    # Also override format_options to emit sections in the configured order.
    context_class = rich_help_cfg.get("context_class")

    effective_order: list[str] = (
        hcfg.section_order
        if hcfg.section_order
        else _PARITY.get("help", {}).get("section_order", ["commands", "options"])
    )

    def _format_usage_colored(self, ctx: click.Context, formatter: click.HelpFormatter) -> None:
        """Color usage pieces structurally using param types."""
        pieces = self.collect_usage_pieces(ctx)
        no_col = getattr(formatter, "_no_color", False)
        if no_col:
            formatter.write_usage(ctx.command_path, " ".join(pieces))
        else:
            arg_color = _ansi_rgb(_ARG_COLOR)
            colored = [arg_color + p + _ANSI_RESET for p in pieces]
            formatter.write_usage(ctx.command_path, " ".join(colored))

    groups_cfg = hcfg.groups

    def _build_command_row(name: str, cmd: click.Command, limit: int) -> tuple[str, str]:
        term = name
        has_subcmds = isinstance(cmd, click.MultiCommand)
        if hasattr(cmd, "params"):
            for p in cmd.params:
                if isinstance(p, click.Argument):
                    if p.required:
                        term += f" <{p.name}>"
                    else:
                        term += f" [{p.name}]"
        if has_subcmds:
            term += " [command]"
        elif hasattr(cmd, "params") and any(
            isinstance(p, click.Option) and p.name != "help" for p in cmd.params
        ):
            term += " [--flags]"
        return (term, cmd.get_short_help_str(limit))

    def _format_commands_with_args(
        self, ctx: click.Context, formatter: click.HelpFormatter
    ) -> None:
        """Override format_commands to show args/subcommands like Go/fang."""
        commands = []
        for cmd_name in self.list_commands(ctx):
            cmd = self.get_command(ctx, cmd_name)
            if cmd is None or cmd.hidden:
                continue
            commands.append((cmd_name, cmd))

        if not commands:
            return

        show_all = ctx.params.get("help_all", False)
        help_group = ctx.params.get("help_group")
        limit = formatter.width - 6 - max(len(c[0]) for c in commands)

        if not groups_cfg:
            rows = [_build_command_row(n, c, limit) for n, c in commands]
            if rows:
                with formatter.section("Commands"):
                    formatter.write_dl(rows)
            return

        group_map: dict[str, list[tuple[str, str]]] = {g.id: [] for g in groups_cfg}
        default_id = groups_cfg[0].id
        for cmd_name, cmd in commands:
            gid = _command_groups.get(cmd_name, default_id)
            if gid not in group_map:
                gid = default_id
            group_map[gid].append(_build_command_row(cmd_name, cmd, limit))

        for g in groups_cfg:
            # per-group filter: show only the requested group
            if help_group and g.id != help_group:
                continue
            if g.hidden and not show_all and not help_group:
                continue
            rows = group_map.get(g.id, [])
            if rows:
                with formatter.section(g.title):
                    formatter.write_dl(rows)

    def _format_options_ordered(self, ctx: click.Context, formatter: click.HelpFormatter) -> None:
        """Emit help sections in the order from HelpConfig or parity.json.

        Section names use fang vocabulary; 'flags'/'global flags' map to Click options.
        """
        import typer.core as _tc

        for section in effective_order:
            if section == "commands":
                self.format_commands(ctx, formatter)
            elif section in ("flags", "global flags", "options"):
                _tc._typer_format_options(self, ctx=ctx, formatter=formatter)

    extra_attrs: dict = {
        "format_commands": _format_commands_with_args,
        "format_options": _format_options_ordered,
        "format_usage": _format_usage_colored,
        "context_class": context_class,
    }

    BrandGroup = type(
        "_BrandTyperGroup",
        (typer.core.TyperGroup,),
        extra_attrs,
    )

    # rich_markup_mode=None: disable Typer's Rich-based help renderer so
    # Click's HelpFormatter pipeline is used instead.
    full_help = f"{help}\n\n{hcfg.disclaimer}" if hcfg.disclaimer else help

    app = typer.Typer(
        name=name,
        help=full_help,
        add_completion=False,
        no_args_is_help=True,
        rich_markup_mode=None,
        cls=BrandGroup,
    )

    # Build the root callback dynamically so we can conditionally include
    # built-in flags and inject GlobalFlag entries without hard-coding them.
    _extra_flags: list[GlobalFlag] = globals or []

    # Assemble parameter list for the dynamic callback function.
    # Always present: ctx, ver (version).
    # Conditionally present: format, quiet, no_color (if not disabled).
    # Extra: one param per GlobalFlag entry.
    params: dict = {}

    # --version (always present, eager)
    params["ver"] = (
        Optional[bool],
        typer.Option(
            None,
            "-v",
            "--version",
            help=f"Print {name} version and exit",
            is_eager=True,
        ),
    )

    # -V / --verbose: stackable count flag
    params["verbose"] = (
        int,
        typer.Option(
            0,
            "-V",
            "--verbose",
            count=True,
            help="Increase log verbosity (-V=debug, -VV=trace)",
        ),
    )

    # --format (unless disabled)
    if not dis.format:
        params["format"] = (
            Optional[str],
            typer.Option(None, "--format", help="Output format"),
        )

    # --quiet (unless disabled)
    if not dis.quiet:
        params["quiet"] = (
            Optional[bool],
            typer.Option(None, "--quiet", help="Suppress non-essential output"),
        )

    # --no-color (unless disabled)
    if not dis.no_color:
        params["no_color"] = (
            Optional[bool],
            typer.Option(None, "--no-color", help="Disable ANSI color output"),
        )

    # --no-hints (unless hints disabled)
    if not dis.hints:
        params["no_hints"] = (
            Optional[bool],
            typer.Option(None, "--no-hints", help="Suppress next-step hints after command output"),
        )

    # --stream (comma-separated named diagnostic streams)
    params["stream"] = (
        str,
        typer.Option("", "--stream", help="Enable diagnostic streams (comma-separated)"),
    )

    # --help-all (when groups configured)
    if hcfg.groups:
        params["help_all"] = (
            Optional[bool],
            typer.Option(
                None,
                "--help-all",
                help="Show all commands including hidden groups",
                is_eager=True,
            ),
        )

    # --help-<id> per-group flags (when groups configured)
    _group_ids: list[str] = []
    if hcfg.groups:
        for g in hcfg.groups:
            _group_ids.append(g.id)
            param_name = f"help_{g.id}"
            params[param_name] = (
                Optional[bool],
                typer.Option(
                    None,
                    f"--help-{g.id}",
                    help=f"Show only {g.title} commands",
                    is_eager=True,
                ),
            )

    # Extra GlobalFlag entries
    for gf in _extra_flags:
        py_name = gf.name.replace("-", "_")
        option_args = [f"--{gf.name}"]
        if gf.short:
            option_args.insert(0, f"-{gf.short}")
        params[py_name] = (
            str,
            typer.Option(gf.default or "", *option_args, help=gf.usage),
        )

    # Build function signature dynamically.
    sig_params = [
        inspect.Parameter("ctx", inspect.Parameter.POSITIONAL_OR_KEYWORD, annotation=typer.Context),
    ]
    for pname, (ptype, pdefault) in params.items():
        sig_params.append(
            inspect.Parameter(
                pname,
                inspect.Parameter.POSITIONAL_OR_KEYWORD,
                default=pdefault,
                annotation=ptype,
            )
        )

    def _root(**kwargs) -> None:  # type: ignore[override]
        ctx_ = kwargs.get("ctx")
        ver_ = kwargs.get("ver")
        help_all_ = kwargs.get("help_all")
        v = kwargs.get("verbose", 0)
        q = bool(kwargs.get("quiet"))
        _quiet_flag.set(q)
        _verbose_count.set(0 if q else v)
        # Wire --stream: parse comma-separated names into enabled set.
        stream_val = kwargs.get("stream", "")
        if stream_val:
            es = _get_enabled_streams()
            # Determine invoked subcommand name for scoping.
            cmd_name = ""
            if ctx_ is not None:
                cmd_name = ctx_.invoked_subcommand or ""
            enabled_set = es.setdefault(cmd_name, set())
            for sname in stream_val.split(","):
                sname = sname.strip()
                if sname:
                    enabled_set.add(sname)
        if ver_:
            v = version if version.startswith("v") else f"v{version}"
            typer.echo(f"{name} {v}")
            raise typer.Exit()
        if help_all_:
            if ctx_ is not None:
                ctx_.params["help_all"] = True
                typer.echo(ctx_.get_help())
            raise typer.Exit()
        # per-group --help-<id> flags
        for gid in _group_ids:
            if kwargs.get(f"help_{gid}"):
                if ctx_ is not None:
                    ctx_.params["help_group"] = gid
                    typer.echo(ctx_.get_help())
                raise typer.Exit()
        if ctx_ is not None and ctx_.invoked_subcommand is None:
            typer.echo(ctx_.get_help())
            raise typer.Exit()

    # Rewrite __signature__ so Typer/Click can introspect the params.
    _root.__signature__ = inspect.Signature(sig_params)  # type: ignore[attr-defined]
    # Also set __annotations__ for Typer's annotation-based introspection.
    _root.__annotations__ = {"ctx": typer.Context}
    for pname, (ptype, _) in params.items():
        _root.__annotations__[pname] = ptype

    app.callback(invoke_without_command=True)(_root)

    # `help <group-id>` subcommand (when groups configured)
    if hcfg.groups:

        @app.command("help", hidden=True)
        def _help_cmd(
            ctx: typer.Context,
            group: str = typer.Argument(..., help="Group ID or 'all'"),
        ) -> None:
            """Show help for a specific command group."""
            parent = ctx.parent
            if parent is None:
                raise typer.Exit(1)
            if group == "all":
                parent.params["help_all"] = True
            elif group in _group_ids:
                parent.params["help_group"] = group
            else:
                typer.echo(
                    f"Unknown group '{group}'. Available: {', '.join(_group_ids)}, all",
                    err=True,
                )
                raise typer.Exit(1)
            typer.echo(parent.get_help())
            raise typer.Exit()

    # Shell completion subcommand (management group, when groups configured)
    if hcfg.groups:
        _register_completion(app, name)
        set_command_group("completion", "management")

    # Wire the full output flag suite (--format-opt/--format-help/--cols/
    # --columns/--template/--output|-o) onto every subcommand defined after
    # this point. Root-level --format remains for legacy parity with apps
    # that read ctx.params['format']; subcommand-level --format from
    # register_output_flags lives at a different scope and does not clash.
    if not dis.format:
        from hop_top_kit.output.cli import register_output_flags

        register_output_flags(app)

    return app, theme


def _register_completion(app: typer.Typer, prog_name: str) -> None:
    """Add ``completion show|install`` subcommands to *app*."""
    from click.shell_completion import get_completion_class

    _SHELLS = ("bash", "zsh", "fish")

    completion_app = typer.Typer(
        name="completion",
        help="Shell completion scripts",
        no_args_is_help=True,
        add_completion=False,
    )

    def _complete_var(name: str) -> str:
        return "_{}_COMPLETE".format(name.replace("-", "_").upper())

    @completion_app.command("show")
    def _show(
        shell: str = typer.Argument(..., help="Shell type (bash, zsh, fish)"),
    ) -> None:
        """Print completion script to stdout."""
        comp_cls = get_completion_class(shell)
        if comp_cls is None:
            typer.echo(f"Unsupported shell: {shell}", err=True)
            raise typer.Exit(1)
        comp = comp_cls(
            cli=None,  # type: ignore[arg-type]
            ctx_args={},
            prog_name=prog_name,
            complete_var=_complete_var(prog_name),
        )
        typer.echo(comp.source())

    @completion_app.command("install")
    def _install(
        shell: str = typer.Argument(..., help="Shell type (bash, zsh, fish)"),
    ) -> None:
        """Install completion script to shell rc file."""
        from typer._completion_shared import (
            install_bash,
            install_fish,
            install_zsh,
        )

        complete_var = _complete_var(prog_name)
        installers = {
            "bash": install_bash,
            "zsh": install_zsh,
            "fish": install_fish,
        }
        installer = installers.get(shell)
        if installer is None:
            typer.echo(f"Unsupported shell: {shell}", err=True)
            raise typer.Exit(1)
        path = installer(
            prog_name=prog_name,
            complete_var=complete_var,
            shell=shell,
        )
        typer.echo(f"{shell} completion installed in {path}")

    app.add_typer(completion_app, name="completion")
