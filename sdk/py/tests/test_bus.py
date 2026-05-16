"""Tests for hop_top_kit.bus — MQTT-style in-memory pub/sub."""

from datetime import UTC, datetime

import pytest

from hop_top_kit.bus import (
    Adapter,
    Bus,
    Event,
    MemoryAdapter,
    create_bus,
    create_event,
)


class TestCreateEvent:
    def test_sets_fields_and_timestamp(self):
        before = datetime.now(UTC)
        e = create_event("llm.request", "src", {"model": "claude"})
        after = datetime.now(UTC)

        assert e.topic == "llm.request"
        assert e.source == "src"
        assert e.payload == {"model": "claude"}
        assert before <= e.timestamp <= after


class TestBusPublishSubscribe:
    def test_delivers_event_to_exact_match(self):
        bus = create_bus()
        got: list[Event] = []
        bus.subscribe("test.event", lambda e: got.append(e))

        bus.publish(create_event("test.event", "src", "hello"))

        assert len(got) == 1
        assert got[0].source == "src"

    def test_no_delivery_to_non_matching(self):
        bus = create_bus()
        called = False

        def handler(e: Event):
            nonlocal called
            called = True

        bus.subscribe("llm.request", handler)
        bus.publish(create_event("llm.response", "src", None))
        assert not called


class TestWildcardStar:
    def test_star_matches_one_segment(self):
        bus = create_bus()
        got: list[str] = []
        bus.subscribe("llm.*", lambda e: got.append(e.topic))

        bus.publish(create_event("llm.request", "src", None))
        bus.publish(create_event("llm.response", "src", None))
        bus.publish(create_event("llm.request.start", "src", None))
        bus.publish(create_event("tool.exec", "src", None))

        assert got == ["llm.request", "llm.response"]

    def test_star_in_any_position(self):
        bus = create_bus()
        got: list[str] = []
        bus.subscribe("*.request", lambda e: got.append(e.topic))

        bus.publish(create_event("llm.request", "src", None))
        bus.publish(create_event("tool.request", "src", None))
        bus.publish(create_event("llm.response", "src", None))

        assert got == ["llm.request", "tool.request"]


class TestWildcardHash:
    def test_hash_matches_zero_or_more_trailing(self):
        bus = create_bus()
        got: list[str] = []
        bus.subscribe("llm.#", lambda e: got.append(e.topic))

        bus.publish(create_event("llm.request", "src", None))
        bus.publish(create_event("llm.request.start", "src", None))
        bus.publish(create_event("llm", "src", None))
        bus.publish(create_event("tool.exec", "src", None))

        assert got == ["llm.request", "llm.request.start", "llm"]

    def test_hash_alone_matches_everything(self):
        bus = create_bus()
        got: list[str] = []
        bus.subscribe("#", lambda e: got.append(e.topic))

        bus.publish(create_event("llm.request", "src", None))
        bus.publish(create_event("anything", "src", None))

        assert got == ["llm.request", "anything"]


class TestUnsubscribe:
    def test_unsubscribe_stops_delivery(self):
        bus = create_bus()
        count = 0

        def handler(e: Event):
            nonlocal count
            count += 1

        unsub = bus.subscribe("test.event", handler)
        bus.publish(create_event("test.event", "src", None))
        assert count == 1

        unsub()
        bus.publish(create_event("test.event", "src", None))
        assert count == 1


class TestMultipleSubscribers:
    def test_all_receive_events(self):
        bus = create_bus()
        a = 0
        b = 0

        def ha(e: Event):
            nonlocal a
            a += 1

        def hb(e: Event):
            nonlocal b
            b += 1

        bus.subscribe("test.event", ha)
        bus.subscribe("test.event", hb)
        bus.publish(create_event("test.event", "src", None))

        assert a == 1
        assert b == 1


class TestClose:
    def test_close_prevents_publish(self):
        bus = create_bus()
        bus.close()

        with pytest.raises(RuntimeError, match="closed"):
            bus.publish(create_event("test", "src", None))


class TestMemoryAdapter:
    def test_implements_adapter_protocol(self):
        adapter = MemoryAdapter()
        assert isinstance(adapter, Adapter)

    def test_publish_subscribe(self):
        adapter = MemoryAdapter()
        got: list[Event] = []
        adapter.subscribe("test.event", lambda e: got.append(e))
        adapter.publish(create_event("test.event", "src", "hello"))

        assert len(got) == 1
        assert got[0].source == "src"
        adapter.close()


class TestBusWithAdapter:
    def test_accepts_custom_adapter(self):
        adapter = MemoryAdapter()
        bus = create_bus(adapter=adapter)
        got: list[Event] = []
        bus.subscribe("test.event", lambda e: got.append(e))
        bus.publish(create_event("test.event", "src", "hello"))

        assert len(got) == 1
        bus.close()

    def test_defaults_to_memory_adapter(self):
        bus = create_bus()
        got: list[Event] = []
        bus.subscribe("x", lambda e: got.append(e))
        bus.publish(create_event("x", "src", None))

        assert len(got) == 1
        bus.close()

    def test_bus_constructor_accepts_adapter(self):
        bus = Bus(adapter=MemoryAdapter())
        got: list[Event] = []
        bus.subscribe("y", lambda e: got.append(e))
        bus.publish(create_event("y", "src", None))

        assert len(got) == 1
        bus.close()
