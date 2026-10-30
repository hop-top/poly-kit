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
from collections.abc import Callable, Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any, Literal, Protocol

import yaml


class _CommandGroup(Protocol):
    """A Click or typer group: anything with Click's lookup hooks."""

    def get_command(self, ctx: Any, cmd_name: str) -> Any: ...

    def list_commands(self, ctx: Any) -> list[str]: ...


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
        """Raise ValueError if name is invalid for an alias.

        See :func:`validate_alias_name`, which needs no config files.
        """
        validate_alias_name(name, builtins=self._cfg.builtins)


# -- standalone validation and cycle detection ------------------------------

AliasNameErrorKind = Literal["empty", "whitespace", "builtin", "duplicate"]

# Unicode White_Space: str.isspace() also counts U+001C..U+001F, which
# the property does not.
_WHITE_SPACE = frozenset(
    "\t\n\x0b\x0c\r \x85\xa0\u1680\u2000\u2001\u2002\u2003\u2004\u2005"
    "\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
)


class AliasNameError(ValueError):
    """An alias name :func:`validate_alias_name` refused."""

    def __init__(self, kind: AliasNameErrorKind, name: str, target: str | None = None) -> None:
        self.kind: AliasNameErrorKind = kind
        self.name = name
        self.target = target
        super().__init__(_alias_name_message(kind, name, target))


def _alias_name_message(kind: AliasNameErrorKind, name: str, target: str | None) -> str:
    if kind == "empty":
        return "alias name must not be empty"
    if kind == "whitespace":
        return "alias name must not contain whitespace"
    if kind == "builtin":
        return f"alias {name!r} conflicts with a built-in command"
    return f"alias {name!r} is already registered for {target!r}"


def validate_alias_name(
    name: str,
    *,
    builtins: Iterable[str] = (),
    existing: Mapping[str, str] | None = None,
) -> None:
    """Check an alias name without a store, config file or command tree.

    Rules run in order and the first failure raises
    :class:`AliasNameError` (a ``ValueError``): empty, whitespace (any
    Unicode White_Space character), builtin (``name`` is one of
    ``builtins``), duplicate (``name`` is a key of ``existing``).
    """
    if not name:
        raise AliasNameError("empty", name)
    if any(c in _WHITE_SPACE for c in name):
        raise AliasNameError("whitespace", name)
    if name in set(builtins):
        raise AliasNameError("builtin", name)
    if existing is not None and name in existing:
        raise AliasNameError("duplicate", name, existing[name])


def detect_cycles(aliases: Mapping[str, str]) -> list[list[str]]:
    """Report alias cycles in ``aliases`` (name -> target).

    An alias points at another when the first word of its target is a
    key of the map. Each cycle is a path starting at its smallest name
    and ending with it again (``["a", "b", "a"]``); cycles are sorted by
    first name. An alias that only leads into a cycle is not part of one.
    """

    def nxt(name: str) -> str | None:
        words = aliases[name].split()
        return words[0] if words and words[0] in aliases else None

    done: set[str] = set()
    cycles: list[list[str]] = []
    for start in sorted(aliases):
        if start in done:
            continue
        pos: dict[str, int] = {}
        walk: list[str] = []
        cur: str | None = start
        while cur is not None and cur not in done:
            if cur in pos:
                members = walk[pos[cur] :]
                low = members.index(min(members))
                path = members[low:] + members[:low]
                cycles.append([*path, path[0]])
                break
            pos[cur] = len(walk)
            walk.append(cur)
            cur = nxt(cur)
        done.update(walk)
    return sorted(cycles, key=lambda c: c[0])


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


AliasHelpMode = Literal["own", "passthrough"]
AliasDispatch = Callable[[str, str, list[str]], Any]


def bridge_to_click(
    group: _CommandGroup,
    store: str | os.PathLike[str] | Mapping[str, str],
    *,
    hidden: bool | None = None,
    describe: Callable[[str, str], str] | None = None,
    help_mode: AliasHelpMode | None = None,
    dispatch: AliasDispatch | Literal[False] | None = None,
) -> None:
    """Patch a Click or typer group's get_command to resolve aliases.

    Uses Click's AliasedGroup pattern: overrides get_command() so
    aliases resolve natively, and list_commands() so aliases appear
    in shell completions for free. Without options a single-word
    target resolves to the real command; a multi-word target resolves
    to a hidden proxy named after the alias, ``Alias for <target>``,
    that owns its ``--help`` and runs the full target path; Click
    leaves it out of completion, as it does any hidden command.

    ``store`` is a config file path (aliases under its ``aliases`` key)
    or an alias map, so aliases can come from any storage.

    Setting any keyword option resolves every alias to a proxy instead,
    a command named after the alias:

    - ``hidden``: hide proxies from help (default True).
    - ``describe(name, target)``: a proxy's help text, in place of
      ``Alias for <target>``.
    - ``help_mode``: ``"own"`` gives the proxy its own ``--help``;
      ``"passthrough"`` (the default) hands ``--help`` to the target, or
      to ``dispatch``.
    - ``dispatch(name, target, args)``: run instead of the target, with
      every argument after the alias unparsed; ``False`` registers
      proxies that run nothing. By default a proxy dispatches its full
      target path through the group.
    """
    if isinstance(store, Mapping):
        aliases = dict(store)
    else:
        try:
            aliases = load_from(os.fspath(store))
        except (FileNotFoundError, OSError):
            return
    if not aliases:
        return

    original_get_command = group.get_command
    original_list_commands = group.list_commands
    use_proxies = any(o is not None for o in (hidden, describe, help_mode, dispatch))
    proxies: dict[str, Any] = {}

    def proxy_for(name: str, target: str) -> Any:
        if name not in proxies:
            proxies[name] = _alias_proxy(
                group,
                name,
                target,
                hidden=True if hidden is None else hidden,
                description=describe(name, target) if describe else f"Alias for {target}",
                # The default path's proxies own --help, as in Go.
                own_help=help_mode == "own" or not use_proxies,
                dispatch=dispatch,
            )
        return proxies[name]

    def get_command_with_aliases(
        ctx: Any,
        cmd_name: str,
    ) -> Any:
        # real command takes precedence
        rv = original_get_command(ctx, cmd_name)
        if rv is not None:
            return rv
        target = aliases.get(cmd_name)
        if not target:
            return None
        parts = target.split()
        if use_proxies or len(parts) > 1:
            return proxy_for(cmd_name, target)
        return original_get_command(ctx, parts[0])

    def list_commands_with_aliases(ctx: Any) -> list[str]:
        cmds = original_list_commands(ctx)
        return sorted(set(cmds) | set(aliases.keys()))

    group.get_command = get_command_with_aliases  # type: ignore[assignment]
    group.list_commands = list_commands_with_aliases  # type: ignore[assignment]


def _alias_proxy(
    group: _CommandGroup,
    name: str,
    target: str,
    *,
    hidden: bool,
    description: str,
    own_help: bool,
    dispatch: AliasDispatch | Literal[False] | None,
) -> Any:
    """Build the command an alias resolves to under options, on the
    Click layer ``group`` runs on."""
    from hop_top_kit._click_compat import make_passthrough_command

    def run(ctx: Any) -> Any:
        args = list(ctx.args)
        if dispatch is False:
            return None
        if dispatch is not None:
            return dispatch(name, target, args)
        # Dispatch the target path through the group, as if typed.
        parent = ctx.parent
        cmd_name, cmd, rest = group.resolve_command(parent, [*target.split(), *args])  # type: ignore[attr-defined]
        with cmd.make_context(cmd_name, rest, parent=parent) as sub:
            return cmd.invoke(sub)

    return make_passthrough_command(
        group,
        name,
        run,
        help=description,
        hidden=hidden,
        add_help_option=own_help,
    )


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
