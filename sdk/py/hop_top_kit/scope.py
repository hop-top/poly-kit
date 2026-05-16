"""hop_top_kit.scope — filesystem path policy guardrails.

Python port of ``hop.top/kit/go/core/scope``. Mirrors the Go API surface
(``new``, ``default``, ``allow``, ``deny``, ``check``, ``enforce``, ``set_mode``,
``set_default``, ``snapshot``, ``secret_paths``) using snake_case naming.

The default deny list is loaded from ``contracts/parity/scope-defaults.json``
(the cross-language source of truth). A local copy is bundled into the
package as ``scope-defaults.json``; ``test_scope.py`` asserts it matches the
canonical contract file.

Glob matching uses `pathspec <https://pypi.org/project/pathspec/>`_ in
GitWildMatch mode, which supports ``**`` recursive matching as needed by
the shared pattern set.

Example::

    from hop_top_kit.scope import default, Op, ErrDenied

    try:
        default().enforce("/Users/me/.ssh/id_rsa", Op.READ)
    except ErrDenied:
        ...  # path denied
"""

from __future__ import annotations

import json
import logging
import os
import sys
from collections.abc import Callable
from dataclasses import dataclass, field
from enum import IntFlag, StrEnum
from pathlib import Path
from typing import Optional

from pathspec import GitIgnoreSpec

__all__ = [
    "Decision",
    "ErrDenied",
    "Mode",
    "Op",
    "Policy",
    "default",
    "new",
    "secret_paths",
    "set_default",
]

_LOG = logging.getLogger(__name__)


# ─── Enums ───────────────────────────────────────────────────────────────────


class Mode(StrEnum):
    """Mode controls how :meth:`Policy.enforce` reacts to a Denied decision."""

    STRICT = "strict"
    """Strict denies → raises :class:`ErrDenied`. The default."""
    WARN = "warn"
    """Warn denies → logs at WARNING level and returns."""
    PROMPT = "prompt"
    """Prompt denies → invokes the prompt callback."""


class Op(IntFlag):
    """Op is a bitset of filesystem operations."""

    READ = 1 << 0
    WRITE = 1 << 1
    EXEC = 1 << 2


class Decision(StrEnum):
    """Decision is the outcome of :meth:`Policy.check`."""

    UNKNOWN = "unknown"
    ALLOWED = "allowed"
    DENIED = "denied"


# ─── Errors ──────────────────────────────────────────────────────────────────


class ErrDenied(Exception):  # noqa: N818  (mirrors Go ErrDenied — kept for cross-language API parity)
    """Raised by :meth:`Policy.enforce` when a path is denied."""

    def __init__(self, path: str, op: Op) -> None:
        super().__init__(f"scope: path denied: {path} (op={_op_string(op)})")
        self.path = path
        self.op = op


# ─── Rule + callback ─────────────────────────────────────────────────────────


@dataclass
class Rule:
    """A single rule: glob patterns + ops bitset + allow/deny verdict."""

    patterns: list[str]
    ops: Op
    allow: bool


PromptFunc = Callable[[str, Op], bool]
"""Prompt callback. Return ``True`` to allow this single call."""


# ─── Defaults JSON ───────────────────────────────────────────────────────────


_DEFAULTS_PATH = Path(__file__).parent / "scope-defaults.json"
_DEFAULTS_RAW = json.loads(_DEFAULTS_PATH.read_text(encoding="utf-8"))


def _platform_key() -> str:
    """Map ``sys.platform`` to the keys used in scope-defaults.json."""
    if sys.platform == "darwin":
        return "darwin"
    if sys.platform == "win32":
        return "windows"
    return "linux"


def _expand_windows_env(p: str) -> str:
    """Expand ``%APPDATA%``, ``%LOCALAPPDATA%``, ``%USERPROFILE%`` from env.

    On non-Windows hosts, the macros are left in place when the env var is
    unset; the pattern simply won't match anything on those hosts.
    """
    for key in ("APPDATA", "LOCALAPPDATA", "USERPROFILE"):
        token = f"%{key}%"
        if token not in p:
            continue
        v = os.environ.get(key, "")
        if not v:
            continue
        p = p.replace(token, v)
    return p


def secret_paths() -> list[str]:
    """Return the default deny pattern set: common + platform-specific.

    Patterns are ready to feed into :meth:`Policy.deny`. ``~`` and Windows
    env macros are resolved at match time (``~`` is left intact here so the
    set is host-portable; ``%FOO%`` is resolved against the current env).
    """
    common = _DEFAULTS_RAW["deny"].get("common", [])
    platform_specific = _DEFAULTS_RAW["deny"].get(_platform_key(), [])
    return [_expand_windows_env(p) for p in (*common, *platform_specific)]


# ─── Policy ──────────────────────────────────────────────────────────────────


@dataclass
class Policy:
    """Holds an ordered list of rules, a Mode, and an optional prompt callback.

    Construct via :func:`new` or :func:`default`.
    """

    _rules: list[Rule] = field(default_factory=list)
    _mode: Mode = Mode.STRICT
    _prompt: Optional[PromptFunc] = None

    # Mutators (return self for chaining) ────────────────────────────────────

    def allow(self, *patterns: str) -> Policy:
        """Register an allow rule covering Read|Write|Exec."""
        return self.allow_op(Op.READ | Op.WRITE | Op.EXEC, *patterns)

    def allow_op(self, op: Op, *patterns: str) -> Policy:
        """Register an allow rule for the given operations."""
        if not patterns:
            return self
        self._rules.append(Rule(patterns=list(patterns), ops=op, allow=True))
        return self

    def deny(self, *patterns: str) -> Policy:
        """Register a deny rule covering Read|Write|Exec."""
        return self.deny_op(Op.READ | Op.WRITE | Op.EXEC, *patterns)

    def deny_op(self, op: Op, *patterns: str) -> Policy:
        """Register a deny rule for the given operations."""
        if not patterns:
            return self
        self._rules.append(Rule(patterns=list(patterns), ops=op, allow=False))
        return self

    def set_mode(self, m: Mode) -> Policy:
        """Set the enforcement mode."""
        self._mode = m
        return self

    def get_mode(self) -> Mode:
        """Return the current enforcement mode."""
        return self._mode

    def set_prompt_func(self, fn: PromptFunc) -> Policy:
        """Set the prompt callback used in :class:`Mode.PROMPT`."""
        self._prompt = fn
        return self

    def get_rules(self) -> list[Rule]:
        """Defensive copy of the rules in registration order."""
        return [Rule(patterns=list(r.patterns), ops=r.ops, allow=r.allow) for r in self._rules]

    # Decision API ────────────────────────────────────────────────────────────

    def check(self, p: str, op: Op) -> Decision:
        """Evaluate (path, op). Pure — no prompt, no mutation.

        Resolves symlinks before matching to defeat symlink escapes; on
        FileNotFoundError, the cleaned path is used so "intent to write"
        still matches deny rules.
        """
        resolved = _resolve_path(p)
        saw_allow = False
        for r in self._rules:
            if (r.ops & op) == 0:
                continue
            if not _match_any(r.patterns, resolved):
                continue
            if not r.allow:
                return Decision.DENIED
            saw_allow = True
        return Decision.ALLOWED if saw_allow else Decision.UNKNOWN

    def enforce(self, p: str, op: Op) -> None:
        """Call :meth:`check` and translate the decision per current Mode.

        - ``Allowed`` → return.
        - ``Unknown`` + non-Strict → return.
        - ``Strict`` + (Denied | Unknown) → raise :class:`ErrDenied`.
        - ``Warn``   + Denied → log + return.
        - ``Prompt`` + Denied → invoke callback; raise on False.
        """
        dec = self.check(p, op)
        if dec is Decision.ALLOWED:
            return
        if dec is Decision.UNKNOWN and self._mode is not Mode.STRICT:
            return
        self._handle_deny(p, op)

    def _handle_deny(self, p: str, op: Op) -> None:
        if self._mode is Mode.WARN:
            _LOG.warning("scope: path denied (warn mode, allowing): %s op=%s", p, _op_string(op))
            return
        if self._mode is Mode.PROMPT:
            if self._prompt is not None and self._prompt(p, op):
                return
            raise ErrDenied(p, op)
        # Strict (default)
        raise ErrDenied(p, op)

    def snapshot(self) -> Policy:
        """Deep copy. Useful for per-test isolation paired with :func:`set_default`."""
        cp = Policy(_mode=self._mode, _prompt=self._prompt)
        cp._rules = [Rule(patterns=list(r.patterns), ops=r.ops, allow=r.allow) for r in self._rules]
        return cp


# ─── Singleton + factory ─────────────────────────────────────────────────────


_default_policy: Optional[Policy] = None


def new() -> Policy:
    """Create an empty :class:`Policy` in :class:`Mode.STRICT`."""
    return Policy()


def default() -> Policy:
    """Return the package-level singleton policy.

    Lazy-initialised on first call. Pre-seeded with :func:`secret_paths` as
    a deny-all-ops rule.
    """
    global _default_policy
    if _default_policy is None:
        _default_policy = new().deny(*secret_paths())
    return _default_policy


def set_default(p: Policy) -> Callable[[], None]:
    """Swap the singleton with ``p``. Returns a restore function.

    Intended for tests::

        restore = set_default(new().allow("~/tmp/**"))
        try:
            ...
        finally:
            restore()
    """
    global _default_policy
    prev = default()
    _default_policy = p

    def restore() -> None:
        global _default_policy
        _default_policy = prev

    return restore


# ─── Path resolution + match ─────────────────────────────────────────────────


def _expand_home(s: str) -> str:
    """Expand a leading ``~`` to the user home dir (resolving symlinks)."""
    if s == "~" or s.startswith("~/"):
        try:
            home = os.path.realpath(os.path.expanduser("~"))
        except OSError:
            home = os.path.expanduser("~")
        return home if s == "~" else os.path.join(home, s[2:])
    return s


def _resolve_path(s: str) -> str:
    """Resolve ``s``: expand ``~``, normalise, follow symlinks.

    On ENOENT walks up to the deepest existing ancestor and re-attaches the
    missing tail so deny rules match by intent.
    """
    expanded = _expand_home(s)
    cleaned = os.path.normpath(expanded)
    try:
        return os.path.realpath(cleaned, strict=True)
    except (FileNotFoundError, OSError):
        pass
    # Walk up.
    head, tail = os.path.split(cleaned)
    head = head.rstrip(os.sep)
    while head and head != os.sep:
        try:
            resolved = os.path.realpath(head, strict=True)
            return os.path.join(resolved, tail)
        except (FileNotFoundError, OSError):
            pass
        head, more = os.path.split(head)
        head = head.rstrip(os.sep)
        tail = os.path.join(more, tail)
    return cleaned


def _match_any(patterns: list[str], abs_path: str) -> bool:
    """Return True if any pattern matches abs_path (already resolved)."""
    for p in patterns:
        expanded = _expand_home(p)
        canon = _canonicalise_pattern(os.path.normpath(expanded))
        if _gitwildmatch(canon, abs_path):
            return True
    return False


def _gitwildmatch(pattern: str, path_str: str) -> bool:
    """Match ``path_str`` against a single gitignore-style ``pattern``."""
    # GitIgnoreSpec expects gitignore-relative semantics: leading "/" anchors
    # to root. To match absolute paths verbatim we strip the leading "/"
    # from both pattern and path before testing.
    p = pattern.lstrip(os.sep)
    a = path_str.lstrip(os.sep)
    spec = GitIgnoreSpec.from_lines([p])
    return bool(spec.match_file(a))


_GLOB_META = set("*?[]{}")


def _canonicalise_pattern(pat: str) -> str:
    """Resolve the leading literal directory prefix of ``pat`` through realpath.

    Mirrors the Go port: ensures ``/tmp/**`` keeps matching after the input
    path is canonicalised (e.g. ``/tmp`` → ``/private/tmp`` on macOS).
    """
    parts = pat.split(os.sep)
    cut = 0
    for i, part in enumerate(parts):
        if any(c in _GLOB_META for c in part):
            break
        cut = i + 1
    if cut == 0:
        return pat
    literal_parts = parts[:cut]
    prefix = os.sep.join(literal_parts)
    if not prefix:
        return pat
    try:
        resolved = os.path.realpath(prefix, strict=True)
        return _join_pattern(resolved, parts[cut:])
    except (FileNotFoundError, OSError):
        for i in range(len(literal_parts) - 1, 0, -1):
            ancestor = os.sep.join(literal_parts[:i])
            if not ancestor:
                continue
            try:
                resolved = os.path.realpath(ancestor, strict=True)
                tail = literal_parts[i:] + parts[cut:]
                return _join_pattern(resolved, tail)
            except (FileNotFoundError, OSError):
                continue
    return pat


def _join_pattern(resolved: str, rest: list[str]) -> str:
    return resolved if not rest else resolved + os.sep + os.sep.join(rest)


def _op_string(op: Op) -> str:
    parts: list[str] = []
    if op & Op.READ:
        parts.append("read")
    if op & Op.WRITE:
        parts.append("write")
    if op & Op.EXEC:
        parts.append("exec")
    return "|".join(parts) if parts else "none"
