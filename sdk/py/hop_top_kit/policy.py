"""policy — the delegation-safety policy and its per-invocation engine.

Ports Go ``console/cli/policy`` (policy, engine, loader, the shipped
``kit-default``). A policy says which side-effect classes may run, how many
mutating operations one invocation may perform, and which commands always
ask for confirmation.

Not ported: ``permissions:`` rules are carried and shape-checked but not
evaluated (Go's expression evaluator has no Python counterpart), and
``callers:`` rules are carried and validated but not evaluated: the CLI
never has a transport-established caller, so only the policy's own rules
answer, exactly as Go answers ``AuthorizeFor(cmd, nil)``.
"""

from __future__ import annotations

import re
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from hop_top_kit import sideeffect, xdg

KIT_DEFAULT_NAME = "kit-default"

_NAME = re.compile(r"[A-Za-z0-9_-]{1,64}")


class PolicyError(Exception):
    """A policy that cannot be resolved, read or validated."""


class MaxOpsExceeded(Exception):  # noqa: N818 - Go's ErrMaxOpsExceeded
    """Raised by :meth:`Engine.record_op` once the budget is spent."""

    def __init__(self) -> None:
        super().__init__("policy: max-ops budget exceeded")


@dataclass
class CallerRule:
    principal: str = ""
    tenant: str = ""
    scope: str = ""
    allow: dict[str, list[str]] = field(default_factory=dict)
    max_ops: int = 0
    window: str = ""
    require_declared_scope: list[str] = field(default_factory=list)


@dataclass
class Policy:
    name: str = ""
    allow: dict[str, list[str]] = field(default_factory=dict)
    max_ops: int = 0
    require_confirm: list[str] = field(default_factory=list)
    permissions: list[dict[str, Any]] = field(default_factory=list)
    unannotated: str = ""
    callers: list[CallerRule] = field(default_factory=list)
    remedy: str = ""

    def validate(self) -> None:
        seen: set[str] = set()
        for i, r in enumerate(self.permissions):
            name = str(r.get("name", ""))
            if not name:
                raise PolicyError(f"permissions[{i}]: name required")
            if name in seen:
                raise PolicyError(f'permission rule "{name}": duplicate name')
            seen.add(name)
            if not r.get("when"):
                raise PolicyError(f"permission rule \"{name}\": 'when' required")
            for fld in ("effect", "otherwise"):
                v = r.get(fld, "")
                if not v:
                    raise PolicyError(f'permission rule "{name}": {fld} required (allow|deny)')
                if v not in ("allow", "deny"):
                    raise PolicyError(
                        f'permission rule "{name}": {fld} "{v}" invalid (want allow|deny)'
                    )
        errs: list[str] = []
        if self.unannotated and sideeffect.class_of(self.unannotated) is None:
            errs.append(f'unannotated: unknown side-effect class "{self.unannotated}"')
        for i, r in enumerate(self.callers):
            for fld, pat in (("principal", r.principal), ("tenant", r.tenant)):
                if path_match(pat, "") is None:
                    errs.append(f'callers[{i}]: {fld} "{pat}": syntax error in pattern')
            errs += [
                f'callers[{i}]: allow: unknown side-effect class "{c}"'
                for c in r.allow
                if sideeffect.class_of(c) is None
            ]
            errs += [
                f'callers[{i}]: require_declared_scope: unknown side-effect class "{c}"'
                for c in r.require_declared_scope
                if sideeffect.class_of(c) is None
            ]
            if r.max_ops < 0:
                errs.append(f"callers[{i}]: max_ops {r.max_ops} is negative; 0 means no budget")
        if errs:
            raise PolicyError("\n".join(errs))


def kit_default() -> Policy:
    """The policy kit ships: reads only for a caller nobody established."""
    return Policy(
        name=KIT_DEFAULT_NAME,
        unannotated="write",
        allow={"write": [], "destructive": []},
        callers=[
            CallerRule(
                allow={"write": ["*"], "destructive": ["*"]},
                require_declared_scope=["destructive"],
            )
        ],
        remedy=(
            "kit's default beyond loopback: writes need an authenticated caller, "
            "destructive commands a declared kit/permissions scope; name a --policy "
            "to choose otherwise"
        ),
    )


@dataclass(frozen=True)
class CommandRef:
    """What the engine reads of a command."""

    verb: str  # command path below the root ("widget add")
    side_effect: str | None = None  # effective tier (declared or inferred)
    permissions: str | None = None


@dataclass(frozen=True)
class Verdict:
    allowed: bool
    require_confirm: bool = False
    reason: str = ""


class Engine:
    """Per-invocation enforcement state; not shared across invocations."""

    def __init__(self, policy: Policy | None = None, max_ops_override: int = 0) -> None:
        self.policy = policy or Policy()
        self.max_ops = max_ops_override if max_ops_override > 0 else self.policy.max_ops
        self.ops_count = 0

    def authorize(self, cmd: CommandRef) -> Verdict:
        se = cmd.side_effect
        if se is None and self.policy.unannotated:
            se = self.policy.unannotated
        if se is None or se == "read":
            return Verdict(True)
        patterns = _lookup_class(self.policy.allow, se)
        if patterns is not None and not match_any(patterns, cmd.verb):
            return self._refuse(f"policy: {se} not allowed for {cmd.verb}")
        return Verdict(True, match_any(self.policy.require_confirm, cmd.verb))

    def _refuse(self, reason: str) -> Verdict:
        if self.policy.remedy:
            reason = f"{reason} (policy {self.policy.name}: {self.policy.remedy})"
        return Verdict(False, False, reason)

    def record_op(self) -> None:
        """Account one successful mutating operation; raises past the budget."""
        self.ops_count += 1
        if self.max_ops > 0 and self.ops_count > self.max_ops:
            raise MaxOpsExceeded


def _lookup_class(allow: dict[str, list[str]], se: str) -> list[str] | None:
    if se in allow:
        return allow[se]
    cls = sideeffect.class_of(se)
    if cls is not None and cls != se:
        return allow.get(cls)
    return None


def match_any(patterns: list[str], value: str) -> bool:
    """``*`` matches anything; others are ``path.Match`` globs; ``verb:*`` is a prefix."""
    for p in patterns:
        if p == "*" or path_match(p, value) is True:
            return True
        if p.endswith(":*"):
            prefix = p[:-2]
            if value == prefix or value.startswith(prefix + " "):
                return True
    return False


def path_match(pattern: str, name: str) -> bool | None:
    """Go's ``path.Match``; ``None`` for a malformed pattern."""
    rx = _glob_regex(pattern)
    if rx is None:
        return None
    return rx.fullmatch(name) is not None


def _glob_regex(pat: str) -> re.Pattern[str] | None:
    out: list[str] = []
    i, n = 0, len(pat)
    while i < n:
        c = pat[i]
        if c == "*":
            out.append("[^/]*")
        elif c == "?":
            out.append("[^/]")
        elif c == "\\":
            i += 1
            if i >= n:
                return None
            out.append(re.escape(pat[i]))
        elif c == "[":
            i += 1
            neg = i < n and pat[i] == "^"
            if neg:
                i += 1
            items: list[str] = []
            while True:
                if i < n and pat[i] == "]" and items:
                    break
                if i >= n:
                    return None
                if pat[i] == "\\":
                    i += 1
                    if i >= n:
                        return None
                lo = pat[i]
                i += 1
                hi = lo
                if i < n and pat[i] == "-":
                    i += 1
                    if i < n and pat[i] == "\\":
                        i += 1
                    if i >= n:
                        return None
                    hi = pat[i]
                    i += 1
                if lo > hi:
                    return None
                items.append(f"{re.escape(lo)}-{re.escape(hi)}")
            out.append(f"[{'^' if neg else ''}{''.join(items)}]")
        else:
            out.append(re.escape(c))
        i += 1
    return re.compile("".join(out), re.DOTALL)


# -- loading -----------------------------------------------------------------


def valid_name(name: str) -> bool:
    return _NAME.fullmatch(name) is not None


def resolve(tool: str, name: str) -> Path:
    """``<config home>/<tool>/policies/<name>.yaml``; the name is checked first."""
    if not tool:
        raise PolicyError("policy: tool name required for XDG resolution")
    if not name:
        raise PolicyError("policy: policy name required")
    if not valid_name(name):
        raise PolicyError(f'policy: invalid policy name "{name}" (want [A-Za-z0-9_-]{{1,64}})')
    return Path(xdg.config_dir(tool)) / "policies" / f"{name}.yaml"


def load_named(tool: str, name: str) -> Policy:
    """Resolve and load; ``kit-default`` is reserved and reads no file."""
    if name == KIT_DEFAULT_NAME:
        return kit_default()
    return load(resolve(tool, name))


def default_policy_loader(tool: str) -> Callable[[str], Policy]:
    """A loader resolving names against ``$XDG_CONFIG_HOME/<tool>/policies/``."""
    return lambda name: load_named(tool, name)


def load(path: Path) -> Policy:
    try:
        data = path.read_text()
    except FileNotFoundError:
        raise PolicyError(f"policy: load {path}: no such file or directory") from None
    except PermissionError:
        raise PolicyError(f"policy: load {path}: permission denied") from None
    except OSError as e:
        raise PolicyError(f"policy: load {path}: {e.strerror or e}") from None
    try:
        p = parse(data)
    except PolicyError as e:
        raise PolicyError(f"policy: parse {path}: {e}") from None
    if not p.name:
        base = path.name
        for ext in (".yaml", ".yml"):
            if base.endswith(ext) and len(base) > len(ext):
                base = base[: -len(ext)]
                break
        p.name = base
    return p


def parse(text: str) -> Policy:
    """Parse and validate YAML policy text."""
    try:
        raw = yaml.safe_load(text)
    except yaml.YAMLError as e:
        raise PolicyError(str(e).replace("\n", " ")) from None
    if raw is None:
        raw = {}
    if not isinstance(raw, dict):
        raise PolicyError("policy document must be a mapping")
    try:
        p = Policy(
            name=str(raw.get("name") or ""),
            allow={str(k): _strs(v) for k, v in (raw.get("allow") or {}).items()},
            max_ops=int(raw.get("max_ops") or 0),
            require_confirm=_strs(raw.get("require_confirm")),
            permissions=[dict(r) for r in (raw.get("permissions") or [])],
            unannotated=str(raw.get("unannotated") or ""),
            callers=[_caller(c) for c in (raw.get("callers") or [])],
        )
    except (TypeError, ValueError, AttributeError) as e:
        raise PolicyError(f"invalid field: {e}") from None
    p.validate()
    return p


def _strs(v: Any) -> list[str]:
    if v is None:
        return []
    if not isinstance(v, list):
        raise TypeError("expected a list")
    return [str(x) for x in v]


def _caller(c: dict[str, Any]) -> CallerRule:
    return CallerRule(
        principal=str(c.get("principal") or ""),
        tenant=str(c.get("tenant") or ""),
        scope=str(c.get("scope") or ""),
        allow={str(k): _strs(v) for k, v in (c.get("allow") or {}).items()},
        max_ops=int(c.get("max_ops") or 0),
        window=str(c.get("window") or ""),
        require_declared_scope=_strs(c.get("require_declared_scope")),
    )
