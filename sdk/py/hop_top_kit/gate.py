"""gate — the delegation gates a leaf passes before its handler runs.

Ports Go ``console/cli/policy_runE.go`` and ``global_dryrun.go`` in Go's
order: ``--dry-run`` enforcement, the named ``--policy``, the ``--confirm``
matrix with the typed ``--confirm-token``, then the ``--max-ops`` budget
accounted after a successful mutating run.

The gate is framework-neutral: :class:`Gate` takes a :class:`Leaf` (what a
decision reads) and :class:`Globals` (the resolved root flags) and either
raises a :class:`~hop_top_kit.output.error.CLIError` or runs the handler.
:mod:`hop_top_kit.cli` wires it into a Typer app; a host may drive it
directly.

Relation to :mod:`hop_top_kit.safety`: that is a Factor-10 ``--force``
check the command author calls. This gate needs no call: it reads
``kit/side-effect`` and refuses before the handler runs.

``--dry-run`` on an *inferred* destructive leaf (annotation absent, name in
the destructive table) never invokes the handler: such a leaf never
promised to honor dry-run, so its handler may really mutate. Kit renders a
preview plan itself.
"""

from __future__ import annotations

import hashlib
import json
import sys
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from enum import Enum
from typing import IO, Any, Protocol, TypeVar

import yaml

from hop_top_kit import policy as pol
from hop_top_kit import sideeffect as se
from hop_top_kit.output.error import (
    TRANSIENCE_TRANSIENT,
    rate_limited_error,
    unauthorized_error,
    usage_error,
)

T = TypeVar("T")

CONFIRM_TOKEN_FLAG = "confirm-token"
SUPERVISOR_COMMAND = "serve"

PolicyLoader = Callable[[str], pol.Policy]


# -- prompting ---------------------------------------------------------------


class Prompter(Protocol):
    def ask(self, question: str) -> str | None:
        """Write ``question``, read one line; lowercased, trimmed; ``None`` at EOF."""


class _Scripted:
    def __init__(self, answers: list[str]) -> None:
        self._answers = list(answers)

    def ask(self, question: str) -> str | None:
        return self._answers.pop(0).strip().lower() if self._answers else None


class _Tty:
    def __init__(self, rd: IO[str], wr: IO[str]) -> None:
        self._rd, self._wr = rd, wr

    def ask(self, question: str) -> str | None:
        self._wr.write(question)
        self._wr.flush()
        line = self._rd.readline()
        return line.strip().lower() if line else None


def _open_tty() -> _Tty:
    """Open the controlling terminal; raises ``OSError`` where there is none."""
    rd = open("/dev/tty")  # noqa: SIM115 - kept for the process lifetime
    wr = open("/dev/tty", "w")  # noqa: SIM115
    if not rd.isatty():
        rd.close()
        wr.close()
        raise OSError("not a terminal")
    return _Tty(rd, wr)


_tty_lock = threading.Lock()
_tty_cache: list[Prompter | None] = []


def _controlling_tty() -> Prompter | None:
    """The process-wide terminal, opened on first use and kept (a second
    prompt must not lose bytes the first buffered)."""
    with _tty_lock:
        if not _tty_cache:
            try:
                _tty_cache.append(_open_tty())
            except OSError:
                _tty_cache.append(None)
        return _tty_cache[0]


class PromptSource:
    """Supplies the terminal a prompt asks on; ``None`` means nobody to ask.

    Interactivity is the terminal's availability, not isatty on stdin, and
    the prompt never reads the command's stdin.
    """

    def __init__(self, fn: Callable[[], Prompter | None]) -> None:
        self._fn = fn

    def get(self) -> Prompter | None:
        return self._fn()

    @classmethod
    def none(cls) -> PromptSource:
        return cls(lambda: None)

    @classmethod
    def from_answers(cls, answers: list[str]) -> PromptSource:
        p = _Scripted(answers)
        return cls(lambda: p)

    @classmethod
    def controlling_tty(cls) -> PromptSource:
        """The process's ``/dev/tty``, opened once; the default."""
        return cls(_controlling_tty)


class ConfirmMode(Enum):
    AUTO = "auto"
    YES = "yes"
    NO = "no"
    PROMPT = "prompt"


def resolve_confirm_mode(src: PromptSource, raw: str) -> ConfirmMode:
    """Parse ``--confirm``; empty or unknown is ``prompt`` with a terminal, else ``no``."""
    try:
        return ConfirmMode(raw.strip().lower())
    except ValueError:
        return ConfirmMode.PROMPT if src.get() is not None else ConfirmMode.NO


def destructive_token(command_path: str) -> str:
    """The token a typed-confirmation command demands: sha256(path)[:6] as hex."""
    return hashlib.sha256(command_path.encode()).digest()[:6].hex()


# -- inputs ------------------------------------------------------------------


@dataclass
class Leaf:
    """Everything a gate decision reads about the leaf being run."""

    program: str
    path: tuple[str, ...]  # below the root
    annotations: Mapping[str, str] = field(default_factory=dict)
    format: str = ""
    confirm_token: str = ""

    @property
    def verb(self) -> str:
        return " ".join(self.path)

    @property
    def full_path(self) -> str:
        return " ".join((self.program, *self.path))

    @property
    def name(self) -> str:
        return self.path[-1] if self.path else ""


@dataclass
class Globals:
    """The resolved root flags. Only the root's values count, never a leaf-local flag."""

    confirm: str = ""
    max_ops: int = 0
    policy: str = ""
    dry_run: bool = False


def requires_token(annotations: Mapping[str, str]) -> bool:
    return annotations.get(se.DESTRUCTIVE_TOKEN) in ("required", "true")


def _machine(fmt: str) -> bool:
    return fmt in ("json", "yaml")


# -- the gate ----------------------------------------------------------------


class Gate:
    def __init__(
        self,
        program: str,
        *,
        policy_loader: PolicyLoader | None = None,
        prompt_source: PromptSource | None = None,
        dry_run_disabled: bool = False,
    ) -> None:
        self.program = program
        self.policy_loader = policy_loader
        self.prompt_source = prompt_source or PromptSource.controlling_tty()
        self.dry_run_disabled = dry_run_disabled

    def engine(self, g: Globals) -> pol.Engine:
        """The engine for an invocation: ``--policy`` through the loader, else an
        empty (default-permit) policy under ``--max-ops``."""
        if g.policy == pol.KIT_DEFAULT_NAME:
            p = pol.kit_default()
        elif g.policy:
            if self.policy_loader is None:
                raise usage_error("--policy is set but no policy loader is wired (policy_loader=)")
            try:
                p = self.policy_loader(g.policy)
            except pol.PolicyError as e:
                # Not an authorization refusal: the gate never ran.
                raise usage_error(f'policy "{g.policy}": {e}') from None
        else:
            p = pol.Policy()
        return pol.Engine(p, g.max_ops)

    def run(
        self,
        leaf: Leaf,
        g: Globals,
        inner: Callable[[], T],
        *,
        engine: pol.Engine | None = None,
        err: IO[str] | None = None,
        out: IO[str] | None = None,
    ) -> T | None:
        """Run ``inner`` behind the gates. Raises an unrendered ``CLIError``.

        ``engine`` lets a host share one budget across several commands (a
        REPL); by default each call builds its own. Returns ``None`` when kit
        previewed an inferred-destructive leaf instead of running it.
        """
        ann = leaf.annotations
        tier = se.effective_side_effect(leaf.name, ann)
        inferred = se.inferred_destructive(leaf.name, ann.get(se.SIDE_EFFECT))

        dry_run = False
        if g.dry_run and not self.dry_run_disabled:
            p = se.resolve_dry_run_policy(leaf.name, ann)
            refusal = se.dry_run_refusal(leaf.full_path, ann, p)
            if refusal is not None:
                raise refusal
            dry_run = p is se.DryRunPolicy.ALLOW

        eng = engine or self.engine(g)

        policy_confirm = False
        if leaf.verb != SUPERVISOR_COMMAND:
            v = eng.authorize(pol.CommandRef(leaf.verb, tier, ann.get(se.PERMISSIONS)))
            if not v.allowed:
                raise unauthorized_error(v.reason)
            policy_confirm = v.require_confirm

        if dry_run and inferred and not ann.get(se.DRY_RUN):
            _render_preview(leaf, out or sys.stdout)
            return None

        if not dry_run and (se.is_destructive_like(tier) or policy_confirm):
            self._confirm(leaf, tier or "", g.confirm, err or sys.stderr)

        with se.dry_run_scope(dry_run):
            result = inner()

        if not dry_run and (se.is_write_like(tier) or se.is_destructive_like(tier)):
            try:
                eng.record_op()
            except pol.MaxOpsExceeded:
                raise rate_limited_error(
                    f"max-ops budget exceeded after running {leaf.full_path}"
                ) from None
        return result

    def _confirm(self, leaf: Leaf, tier: str, raw: str, err: IO[str]) -> None:
        mode = resolve_confirm_mode(self.prompt_source, raw)
        if requires_token(leaf.annotations):
            # Never lets --confirm=yes alone proceed.
            expected = destructive_token(leaf.full_path)
            got = leaf.confirm_token
            if not got:
                refusal = unauthorized_error(
                    f"destructive command {leaf.full_path} requires --confirm-token={expected}"
                )
                if _machine(leaf.format):
                    # The preview would leave stderr unparseable; the envelope
                    # carries the instruction instead.
                    refusal.suggested_fix = f"re-run with --confirm-token={expected} to proceed"
                else:
                    err.write(
                        "This is a destructive operation requiring typed confirmation.\n"
                        f"Token: {expected}\n"
                        f"Re-run with --confirm-token={expected} to proceed.\n"
                    )
                raise refusal
            if got != expected:
                raise unauthorized_error(
                    f"--confirm-token mismatch for {leaf.full_path}; expected {expected}"
                )
            return
        if mode in (ConfirmMode.YES, ConfirmMode.AUTO):
            return
        if mode is ConfirmMode.NO:
            # Transient: re-invoking with --confirm=yes clears it.
            raise unauthorized_error(
                f"destructive command {leaf.full_path} refused: --confirm=no (or non-TTY default)"
            ).with_transience(TRANSIENCE_TRANSIENT)
        tty = self.prompt_source.get()
        answer = (
            tty.ask(f"This is a {tier} operation ({leaf.full_path}). Continue? [y/N] ")
            if tty
            else None
        )
        if answer not in ("y", "yes"):
            raise unauthorized_error(
                f"aborted by user at confirm prompt for {leaf.full_path}"
            ).with_transience(TRANSIENCE_TRANSIENT)


def _render_preview(leaf: Leaf, out: IO[str]) -> None:
    plan: dict[str, Any] = {
        "command": leaf.full_path,
        "effects": [
            {
                "kind": "unknown-destructive",
                "target": leaf.full_path,
                "reversible": False,
                "detail": "inferred destructive from the command name",
            }
        ],
        "warnings": [
            f"{leaf.full_path} declares no kit/side-effect and was not run; kit previewed it "
            "because a command named like this may mutate state and never promised dry-run"
        ],
        "generated_at": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    if leaf.format == "json":
        out.write(json.dumps(plan, indent=2) + "\n")
    elif leaf.format == "yaml":
        yaml.safe_dump(plan, out, default_flow_style=False, sort_keys=False)
    else:
        out.write(f"Dry run: {leaf.full_path} (not run)\n")
        for w in plan["warnings"]:
            out.write(f"Warning: {w}\n")


__all__ = [
    "CONFIRM_TOKEN_FLAG",
    "ConfirmMode",
    "Gate",
    "Globals",
    "Leaf",
    "PolicyLoader",
    "PromptSource",
    "Prompter",
    "destructive_token",
    "requires_token",
    "resolve_confirm_mode",
]
