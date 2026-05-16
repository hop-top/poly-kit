"""
hop_top_kit.completion — dynamic value completion system.

Provides a Completer protocol + built-in completers + a registry
for binding completers to flags/args. Bridges to Click's native
shell_complete mechanism.
"""

from __future__ import annotations

import os
from collections.abc import Callable
from dataclasses import dataclass
from typing import Protocol, runtime_checkable

from click.shell_completion import CompletionItem as ClickCompletionItem


@dataclass
class CompletionItem:
    value: str
    description: str = ""


@runtime_checkable
class Completer(Protocol):
    def complete(self, prefix: str) -> list[CompletionItem]: ...


# ---------------------------------------------------------------------------
# Built-in completers
# ---------------------------------------------------------------------------


class _StaticCompleter:
    """Returns items whose value matches prefix (case-insensitive)."""

    def __init__(self, items: tuple[CompletionItem, ...]) -> None:
        self._items = items

    def complete(self, prefix: str) -> list[CompletionItem]:
        low = prefix.lower()
        return [i for i in self._items if i.value.lower().startswith(low)]


def static_completer(*items: CompletionItem) -> Completer:
    return _StaticCompleter(items)


def static_values(*values: str) -> Completer:
    return _StaticCompleter(tuple(CompletionItem(v) for v in values))


class _FuncCompleter:
    def __init__(self, fn: Callable[[str], list[CompletionItem]]) -> None:
        self._fn = fn

    def complete(self, prefix: str) -> list[CompletionItem]:
        return self._fn(prefix)


def func_completer(fn: Callable[[str], list[CompletionItem]]) -> Completer:
    return _FuncCompleter(fn)


class _PrefixedCompleter:
    """Prepends ``dimension:`` to inner completer values."""

    def __init__(self, dimension: str, inner: Completer) -> None:
        self._dim = dimension
        self._inner = inner

    def complete(self, prefix: str) -> list[CompletionItem]:
        # strip dimension prefix from user input before delegating
        inner_prefix = prefix
        dim_prefix = self._dim + ":"
        if prefix.startswith(dim_prefix):
            inner_prefix = prefix[len(dim_prefix) :]

        results = self._inner.complete(inner_prefix)
        return [CompletionItem(f"{self._dim}:{r.value}", r.description) for r in results]


def prefixed_completer(dimension: str, values: Completer) -> Completer:
    return _PrefixedCompleter(dimension, values)


def config_keys_completer(config: dict) -> Completer:
    return _StaticCompleter(tuple(CompletionItem(k) for k in config))


class _FileCompleter:
    def __init__(self, extensions: tuple[str, ...]) -> None:
        self._exts = extensions

    def complete(self, prefix: str) -> list[CompletionItem]:
        directory = os.path.dirname(prefix) or "."
        base = os.path.basename(prefix)
        try:
            entries = os.listdir(directory)
        except OSError:
            return []

        results: list[CompletionItem] = []
        for name in sorted(entries):
            full = os.path.join(directory, name)
            if os.path.isdir(full):
                continue
            if not name.startswith(base):
                continue
            if self._exts and not any(name.endswith(e) for e in self._exts):
                continue
            results.append(CompletionItem(full, "file"))
        return results


def file_completer(*extensions: str) -> Completer:
    return _FileCompleter(extensions)


class _DirCompleter:
    def complete(self, prefix: str) -> list[CompletionItem]:
        directory = os.path.dirname(prefix) or "."
        base = os.path.basename(prefix)
        try:
            entries = os.listdir(directory)
        except OSError:
            return []

        results: list[CompletionItem] = []
        for name in sorted(entries):
            full = os.path.join(directory, name)
            if not os.path.isdir(full):
                continue
            if not name.startswith(base):
                continue
            results.append(CompletionItem(full, "dir"))
        return results


def dir_completer() -> Completer:
    return _DirCompleter()


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------


class CompletionRegistry:
    """Maps flag names and positional args to completers."""

    def __init__(self) -> None:
        self._flags: dict[str, Completer] = {}
        self._args: dict[tuple[str, int], Completer] = {}

    def register(self, flag: str, completer: Completer) -> None:
        self._flags[flag] = completer

    def register_arg(self, cmd: str, pos: int, completer: Completer) -> None:
        self._args[(cmd, pos)] = completer

    def for_flag(self, flag: str) -> Completer | None:
        return self._flags.get(flag)

    def for_arg(self, cmd: str, pos: int) -> Completer | None:
        return self._args.get((cmd, pos))


# ---------------------------------------------------------------------------
# Click bridge
# ---------------------------------------------------------------------------


def to_click_shell_complete(
    completer: Completer,
) -> Callable:
    """Bridge a Completer to Click's ``shell_complete`` callback.

    Returns a callable with signature
    ``(ctx, param, incomplete) -> list[click.shell_completion.CompletionItem]``
    suitable for passing to Click's ``Option(shell_complete=...)``
    or ``Argument(shell_complete=...)``.
    """

    def _bridge(ctx, param, incomplete: str) -> list[ClickCompletionItem]:
        items = completer.complete(incomplete)
        return [ClickCompletionItem(i.value, help=i.description or None) for i in items]

    return _bridge
