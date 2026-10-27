"""
hop_top_kit.llm_toolnames — provider tool-name rules and the reversible
sanitizer.

Providers restrict tool names; callers use ids such as ``pane.open`` or
``capability:files.read``. A tool-calling adapter builds one
:class:`ToolNames` per request: it checks the request's tool names
against the provider's :class:`ToolNameRule`, gives each a name the
provider accepts (its wire name), and maps wire names in returned tool
calls back to the caller's names.

The algorithm is shared by every kit SDK; the normative spec is
``contracts/llm-v1/tool-names.md`` and ``contracts/llm-v1/vectors.json``
(section ``tool_names``) pins it.
"""

from __future__ import annotations

import dataclasses
import hashlib
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from enum import StrEnum

from hop_top_kit.llm import LLMError, ToolCall, ToolDef, ToolNameError, ToolNameReason

__all__ = [
    "ANTHROPIC_TOOL_NAMES",
    "GEMINI_TOOL_NAMES",
    "OPENAI_TOOL_NAMES",
    "TOOL_NAMES_PARAM",
    "ToolNameMode",
    "ToolNameRule",
    "ToolNames",
    "tool_name_mode_of",
]

# Provider param selecting the mode: ``scheme://model?tool_names=strict``.
TOOL_NAMES_PARAM = "tool_names"

# Hex digits of the name's SHA-256 a shortened name ends with, after "_".
_HASH_LEN = 8


class ToolNameMode(StrEnum):
    """How an adapter treats a tool name its provider does not accept."""

    # Default: send the name as ToolNameRule.sanitize rewrites it and map
    # it back in every tool call returned.
    SANITIZE = "sanitize"
    # Send names as given; a name the provider rejects fails the request
    # (reason "invalid") before any network call.
    STRICT = "strict"


def tool_name_mode_of(params: Mapping[str, str]) -> ToolNameMode:
    """Read :data:`TOOL_NAMES_PARAM` from provider params.

    Absent means :attr:`ToolNameMode.SANITIZE`; any value other than
    ``sanitize`` or ``strict`` (surrounding whitespace ignored) raises
    :class:`~hop_top_kit.llm.LLMError`, so adapter construction fails.
    """
    if TOOL_NAMES_PARAM not in params:
        return ToolNameMode.SANITIZE
    raw = params[TOOL_NAMES_PARAM]
    try:
        return ToolNameMode(raw.strip())
    except ValueError:
        raise LLMError(
            f"{TOOL_NAMES_PARAM} must be {ToolNameMode.SANITIZE.value!r} or "
            f"{ToolNameMode.STRICT.value!r}, got {raw!r}"
        ) from None


def _is_alnum(c: int) -> bool:
    return 0x30 <= c <= 0x39 or 0x41 <= c <= 0x5A or 0x61 <= c <= 0x7A


def _utf8(provider: str, name: str) -> bytes:
    """Return the UTF-8 bytes of *name*, or raise the request error."""
    if not name:
        raise ToolNameError(provider, ToolNameReason.EMPTY, name)
    try:
        return name.encode("utf-8")
    except UnicodeEncodeError:  # a lone surrogate
        raise ToolNameError(provider, ToolNameReason.INVALID_UTF8, name) from None


@dataclass(frozen=True)
class ToolNameRule:
    """A provider's rule for tool names.

    1 to ``max_len`` bytes, each an ASCII letter, digit, ``_``, ``-`` or
    one of ``extra``; with ``lead_letter``, the first byte is a letter or
    ``_``. ``provider`` names the rule in errors.
    """

    provider: str
    max_len: int
    extra: str = ""
    lead_letter: bool = False

    def _allowed(self, c: int) -> bool:
        return _is_alnum(c) or c in (0x5F, 0x2D) or chr(c) in self.extra

    def _lead_ok(self, c: int) -> bool:
        return not self.lead_letter or c == 0x5F or (_is_alnum(c) and c > 0x39)

    def _valid_bytes(self, b: bytes) -> bool:
        return (
            0 < len(b) <= self.max_len and self._lead_ok(b[0]) and all(self._allowed(c) for c in b)
        )

    def valid(self, name: str) -> bool:
        """Report whether the provider accepts *name* as is."""
        try:
            return self._valid_bytes(name.encode("utf-8"))
        except UnicodeEncodeError:
            return False

    def sanitize(self, name: str) -> str:
        """Return *name* as the provider accepts it.

        A valid name comes back unchanged. Otherwise, over its UTF-8
        bytes: every maximal run of bytes the rule does not allow becomes
        one ``_``; with ``lead_letter``, a first byte that is not a letter
        or ``_`` gets a ``_`` in front; a result longer than ``max_len``,
        or holding no ASCII letter or digit, keeps its first
        ``max_len - 9`` bytes (all of it when shorter) and gains ``_`` and
        the first 8 lowercase hex digits of SHA-256 of the name.

        Raises :class:`~hop_top_kit.llm.ToolNameError` (``empty`` or
        ``invalid_utf8``); the result is never empty.
        """
        raw = _utf8(self.provider, name)
        if self._valid_bytes(raw):
            return name
        out = bytearray()
        run = False
        for c in raw:
            if self._allowed(c):
                out.append(c)
                run = False
            elif not run:
                out.append(0x5F)
                run = True
        if not self._lead_ok(out[0]):
            out.insert(0, 0x5F)
        if len(out) <= self.max_len and any(_is_alnum(c) for c in out):
            return out.decode("ascii")
        digest = hashlib.sha256(raw).hexdigest()[:_HASH_LEN]
        keep = min(len(out), self.max_len - _HASH_LEN - 1)
        return out[:keep].decode("ascii") + "_" + digest


# ^[a-zA-Z0-9_-]{1,128}$ (Messages API tools[].name).
ANTHROPIC_TOOL_NAMES = ToolNameRule(provider="anthropic", max_len=128)
# ^[a-zA-Z0-9_-]{1,64}$ (Chat Completions function.name), for OpenAI and
# the OpenAI-compatible servers, routellm included.
OPENAI_TOOL_NAMES = ToolNameRule(provider="openai", max_len=64)
# ^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$ (FunctionDeclaration.name).
GEMINI_TOOL_NAMES = ToolNameRule(provider="gemini", max_len=128, extra=".:", lead_letter=True)


class ToolNames:
    """One request's tool names mapped to wire names and back.

    Build with :meth:`build`; adapters make one per request.
    """

    def __init__(self, rule: ToolNameRule, mode: ToolNameMode) -> None:
        self.rule = rule
        self.mode = ToolNameMode(mode)
        self._to_wire: dict[str, str] = {}
        self._to_orig: dict[str, str] = {}

    @classmethod
    def build(cls, rule: ToolNameRule, mode: ToolNameMode, defs: Iterable[ToolDef]) -> ToolNames:
        """Check *defs* against *rule* and build their mapping.

        The first failing def, in order, raises
        :class:`~hop_top_kit.llm.ToolNameError`: ``empty``,
        ``invalid_utf8``, ``invalid`` (strict mode, a name the rule does
        not accept), ``duplicate`` (a name an earlier def has) or
        ``collision`` (a wire name an earlier def has; ``other`` names
        it).
        """
        t = cls(rule, mode)
        for d in defs:
            name = d.name
            if t.mode is ToolNameMode.STRICT:
                _utf8(rule.provider, name)
                if not rule.valid(name):
                    raise ToolNameError(rule.provider, ToolNameReason.INVALID, name)
                wire = name
            else:
                wire = rule.sanitize(name)
            if name in t._to_wire:
                raise ToolNameError(rule.provider, ToolNameReason.DUPLICATE, name)
            other = t._to_orig.get(wire)
            if other is not None:
                raise ToolNameError(rule.provider, ToolNameReason.COLLISION, name, other)
            t._to_wire[name] = wire
            t._to_orig[wire] = name
        return t

    def wire(self, name: str) -> str:
        """Return the name to send for *name*.

        A def's wire name; else, as for a call replayed from history,
        *name* sanitized (sanitize mode) or unchanged (strict mode, or a
        name the sanitizer refuses).
        """
        w = self._to_wire.get(name)
        if w is not None:
            return w
        if self.mode is ToolNameMode.STRICT:
            return name
        try:
            return self.rule.sanitize(name)
        except ToolNameError:
            return name

    def original(self, wire: str) -> str:
        """Return the def name *wire* stands for; unknown names come back unchanged."""
        return self._to_orig.get(wire, wire)

    def defs(self, defs: Iterable[ToolDef]) -> list[ToolDef]:
        """Return copies of *defs* under their wire names."""
        return [dataclasses.replace(d, name=self.wire(d.name)) for d in defs]

    def calls(self, calls: Iterable[ToolCall]) -> list[ToolCall]:
        """Return copies of *calls* carrying the original names."""
        return [dataclasses.replace(c, name=self.original(c.name)) for c in calls]
