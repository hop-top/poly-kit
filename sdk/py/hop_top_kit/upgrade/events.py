"""Migration lifecycle events, published on a :class:`hop_top_kit.bus.Bus`.

Run level: ``run_started``, then ``run_completed`` or ``run_failed``.
``baselined`` follows a baseline stamp of an unversioned schema, before that
schema's migrations. Per migration: ``started``, then ``applied`` (up
succeeded) and ``verified`` (its verify passed), or ``failed`` (stage
``record``: the version could not be recorded). ``reverted`` follows a
rollback that succeeded (``restore`` after a failed ``up``). The final verify publishes ``verified`` with
``final: true`` or ``failed`` with stage ``final``.

Payloads are plain dicts with the Go field names; absent optional fields
are left out. The event source is :data:`SOURCE`.
"""

from __future__ import annotations

import contextlib
import re
import time
from dataclasses import dataclass, fields, replace
from typing import Any

from hop_top_kit.bus import Bus, create_event

#: ``Event.source`` of every migration event.
SOURCE = "core.upgrade"

_ACTIONS = (
    "run_started",
    "run_completed",
    "run_failed",
    "started",
    "applied",
    "verified",
    "failed",
    "reverted",
    "baselined",
)
_SEGMENT = re.compile(r"[a-z0-9_]+")


@dataclass(frozen=True)
class MigrationTopics:
    """The topics a Migrator publishes on; defaults are kit's."""

    run_started: str = "kit.core.migration.run_started"
    run_completed: str = "kit.core.migration.run_completed"
    run_failed: str = "kit.core.migration.run_failed"
    started: str = "kit.core.migration.started"
    applied: str = "kit.core.migration.applied"
    verified: str = "kit.core.migration.verified"
    failed: str = "kit.core.migration.failed"
    reverted: str = "kit.core.migration.reverted"
    baselined: str = "kit.core.migration.baselined"

    @classmethod
    def with_prefix(cls, prefix: str) -> MigrationTopics:
        """Every topic from a 3-segment prefix (``"myapp.core.migration"``);
        :class:`ValueError` when it is not ``source.category.object`` of
        lowercase letters, digits and underscores."""
        parts = prefix.split(".")
        if len(parts) != 3 or not all(_SEGMENT.fullmatch(p) for p in parts):
            raise ValueError(f"upgrade: migration topic prefix {prefix!r}: want 3 segments")
        return cls(**{a: f"{prefix}.{a}" for a in _ACTIONS})

    def replace(self, **topics: str) -> MigrationTopics:
        """A copy with *topics* replaced; empty values keep the current one."""
        known = {f.name for f in fields(self)}
        bad = set(topics) - known
        if bad:
            raise TypeError(f"unknown topics {sorted(bad)}")
        return replace(self, **{k: v for k, v in topics.items() if v})


def millis(since: float) -> int:
    """Whole milliseconds since the ``time.monotonic()`` reading *since*."""
    return int((time.monotonic() - since) * 1000)


class Emitter:
    """Publishes payloads; silent without a bus. Publishing is synchronous,
    so subscribers see events in order; errors are dropped and never fail
    a run."""

    def __init__(self, bus: Bus | None, topics: MigrationTopics) -> None:
        self.bus = bus
        self.topics = topics

    def emit(self, name: str, payload: dict[str, Any]) -> None:
        topic = getattr(self.topics, name)
        if self.bus is None or not topic:
            return
        with contextlib.suppress(Exception):  # a subscriber never fails a run
            self.bus.publish(create_event(topic, SOURCE, payload))


def migration_payload(
    tool: str,
    schema: str,
    from_: str,
    to: str,
    *,
    stage: str = "",
    method: str = "",
    final: bool = False,
    error: str = "",
    duration_ms: int | None = None,
) -> dict[str, Any]:
    """A per-migration payload: ``tool``, ``schema``, ``from``, ``to`` always;
    the rest only when set."""
    p: dict[str, Any] = {"tool": tool, "schema": schema, "from": from_, "to": to}
    if stage:
        p["stage"] = stage
    if method:
        p["method"] = method
    if final:
        p["final"] = True
    if error:
        p["error"] = error
    if duration_ms is not None:
        p["duration_ms"] = duration_ms
    return p


def run_payload(
    tool: str,
    to: str,
    *,
    applied: int | None = None,
    schema: str = "",
    error: str = "",
    duration_ms: int | None = None,
) -> dict[str, Any]:
    """A run-level payload: ``tool`` and ``to`` always; the rest when set."""
    p: dict[str, Any] = {"tool": tool, "to": to}
    if applied is not None:
        p["applied"] = applied
    if schema:
        p["schema"] = schema
    if error:
        p["error"] = error
    if duration_ms is not None:
        p["duration_ms"] = duration_ms
    return p
