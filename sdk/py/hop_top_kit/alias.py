"""
hop_top_kit.alias — YAML-file-based command alias expansion.

Three-tier priority: seeded (built-in) < global (user) < local (project).

Usage::

    expander = Expander(Config(
        global_path="~/.config/mytool/config.yaml",
        local_path=".mytool/config.yaml",
        seeded_aliases={"setup": "config interactive"},
    ))
    args, expanded = expander.expand(sys.argv)
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field

import click
import yaml


@dataclass
class Config:
    """Configures the Expander."""

    global_path: str = ""
    local_path: str = ""
    seeded_aliases: dict[str, str] = field(default_factory=dict)
    builtins: set[str] = field(default_factory=set)


class Expander:
    """Loads and expands YAML-file-based command aliases."""

    def __init__(self, cfg: Config) -> None:
        self._cfg = cfg

    def load(self) -> dict[str, str]:
        """Return merged alias map (seeded < global < local)."""
        try:
            gl = load_from(self._cfg.global_path)
        except (FileNotFoundError, OSError):
            gl = {}

        try:
            loc = load_from(self._cfg.local_path)
        except (FileNotFoundError, OSError):
            loc = {}

        merged: dict[str, str] = {}
        merged.update(self._cfg.seeded_aliases)
        merged.update(gl)
        merged.update(loc)
        return merged

    def expand(self, args: list[str]) -> tuple[list[str], bool]:
        """Rewrite args if first non-flag arg matches an alias.

        Returns (possibly rewritten args, whether expansion occurred).
        """
        if len(args) < 2:
            return args, False

        aliases = self.load()
        if not aliases:
            return args, False

        idx, candidate = find_first_non_flag(args[1:])
        if not candidate:
            return args, False

        expansion = aliases.get(candidate)
        if expansion is None:
            return args, False

        parts = expansion.split()
        prefix = args[1 : 1 + idx]
        suffix = args[1 + idx + 1 :]
        result = [args[0], *prefix, *parts, *suffix]
        return result, True

    def validate_name(self, name: str) -> None:
        """Raise ValueError if name is invalid for an alias."""
        if not name:
            raise ValueError("alias name must not be empty")
        if any(c in name for c in (" ", "\t", "\n")):
            raise ValueError("alias name must not contain whitespace")
        if name in self._cfg.builtins:
            raise ValueError(f"alias {name!r} conflicts with a built-in command")


def load_from(path: str) -> dict[str, str]:
    """Read aliases from a single YAML config file."""
    with open(path) as f:
        raw = yaml.safe_load(f)
    if not isinstance(raw, dict):
        return {}
    return dict(raw.get("aliases") or {})


def save_to(path: str, aliases: dict[str, str]) -> None:
    """Write aliases into a YAML config, preserving other keys."""
    os.makedirs(os.path.dirname(path), exist_ok=True)

    existing: dict = {}
    try:
        with open(path) as f:
            existing = yaml.safe_load(f) or {}
    except FileNotFoundError:
        pass

    if aliases:
        existing["aliases"] = dict(aliases)
    else:
        existing.pop("aliases", None)

    with open(path, "w") as f:
        yaml.dump(existing, f, default_flow_style=False)


def bridge_to_click(group: click.Group, store_path: str) -> None:
    """Patch a Click group's get_command to resolve aliases.

    Uses Click's AliasedGroup pattern: overrides get_command() so
    aliases resolve natively, and list_commands() so aliases appear
    in shell completions for free.
    """
    try:
        aliases = load_from(store_path)
    except (FileNotFoundError, OSError):
        return
    if not aliases:
        return

    original_get_command = group.get_command
    original_list_commands = group.list_commands

    def get_command_with_aliases(
        ctx: click.Context,
        cmd_name: str,
    ) -> click.Command | None:
        # real command takes precedence
        rv = original_get_command(ctx, cmd_name)
        if rv is not None:
            return rv
        target = aliases.get(cmd_name)
        if not target:
            return None
        parts = target.split()
        return original_get_command(ctx, parts[0])

    def list_commands_with_aliases(ctx: click.Context) -> list[str]:
        cmds = original_list_commands(ctx)
        return sorted(set(cmds) | set(aliases.keys()))

    group.get_command = get_command_with_aliases  # type: ignore[assignment]
    group.list_commands = list_commands_with_aliases  # type: ignore[assignment]


def find_first_non_flag(
    slc: list[str],
) -> tuple[int, str]:
    """Index+value of first non-flag element in slice."""
    i = 0
    while i < len(slc):
        a = slc[i]
        if not a.startswith("-"):
            return i, a
        if "=" in a:
            i += 1
            continue
        # Long flags (--foo) must use --flag=value for values;
        # bare --flag is treated as boolean. Only short flags
        # (-x val) consume the next arg.
        if a.startswith("--"):
            i += 1
            continue
        # short flag without '=': next arg is flag value
        if i + 1 < len(slc) and not slc[i + 1].startswith("-"):
            i += 2
        else:
            i += 1
    return -1, ""
