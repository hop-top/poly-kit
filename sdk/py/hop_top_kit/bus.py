"""In-memory pub/sub with MQTT-style topic matching.

Wildcards (dot-separated segments):
  ``*`` — matches exactly one segment
  ``#`` — matches zero or more trailing segments
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, Protocol, runtime_checkable


@dataclass
class Event:
    topic: str
    source: str
    timestamp: datetime
    payload: Any


def create_event(topic: str, source: str, payload: Any) -> Event:
    return Event(
        topic=topic,
        source=source,
        timestamp=datetime.now(UTC),
        payload=payload,
    )


def _match_topic(topic: str, pattern: str) -> bool:
    """MQTT-style pattern match against a dot-separated topic."""
    t_parts = topic.split(".")
    p_parts = pattern.split(".")

    ti = 0
    pi = 0

    while pi < len(p_parts):
        if p_parts[pi] == "#":
            return pi == len(p_parts) - 1
        if ti >= len(t_parts):
            return False
        if p_parts[pi] != "*" and p_parts[pi] != t_parts[ti]:
            return False
        ti += 1
        pi += 1

    return ti == len(t_parts)


_Handler = Callable[[Event], None]
_Unsubscribe = Callable[[], None]


@dataclass
class _Subscription:
    id: int
    pattern: str
    handler: _Handler


@runtime_checkable
class Adapter(Protocol):
    """Pluggable transport layer for the bus."""

    def publish(self, event: Event) -> None: ...

    def subscribe(self, pattern: str, handler: _Handler) -> _Unsubscribe: ...

    def close(self) -> None: ...


class MemoryAdapter:
    """Default in-process adapter."""

    def __init__(self) -> None:
        self._subs: list[_Subscription] = []
        self._next_id: int = 0
        self._closed: bool = False

    def publish(self, event: Event) -> None:
        if self._closed:
            raise RuntimeError("bus: publish after closed")

        matching = [s for s in self._subs if _match_topic(event.topic, s.pattern)]
        for s in matching:
            s.handler(event)

    def subscribe(self, pattern: str, handler: _Handler) -> _Unsubscribe:
        sub_id = self._next_id
        self._next_id += 1
        self._subs.append(_Subscription(id=sub_id, pattern=pattern, handler=handler))

        def unsub() -> None:
            self._subs = [s for s in self._subs if s.id != sub_id]

        return unsub

    def close(self) -> None:
        self._closed = True
        self._subs = []


class Bus:
    """Pub/sub bus backed by a pluggable Adapter."""

    def __init__(self, adapter: Adapter | None = None) -> None:
        self._adapter = adapter or MemoryAdapter()

    def publish(self, event: Event) -> None:
        self._adapter.publish(event)

    def subscribe(self, pattern: str, handler: _Handler) -> _Unsubscribe:
        return self._adapter.subscribe(pattern, handler)

    def close(self) -> None:
        self._adapter.close()


def create_bus(adapter: Adapter | None = None) -> Bus:
    return Bus(adapter=adapter)
