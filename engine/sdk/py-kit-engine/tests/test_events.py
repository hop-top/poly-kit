"""EventStream speaks the engine-protocol subscribe/message frames."""

from __future__ import annotations

import socket
import time

import pytest

pytest.importorskip("websocket")

from kit_engine.events import EventStream

from .ws_server import Server, until


@pytest.fixture
def srv():
    s = Server()
    yield s
    s.close()


@pytest.fixture
def streams():
    made: list[EventStream] = []
    yield made
    for s in made:
        s.close()


def msg(topic, payload):
    return {"type": "message", "topic": topic, "payload": payload}


def open_stream(streams, url, topic="*", token=None):
    s = EventStream(url, topic, token)
    streams.append(s)
    return s


def test_subscribes_and_decodes_payloads_skipping_control_frames(srv, streams):
    s = open_stream(streams, srv.url, "a.b")
    got = []
    s.on("*", got.append)
    until(lambda: srv.peers and srv.peers[0].received)
    assert srv.peers[0].received[0] == {"type": "subscribe", "topic": "a.b"}
    p = srv.peers[0]
    p.send({"type": "welcome"})
    p.send({"type": "ack", "topic": "a.b"})
    p.send({"type": "error", "topic": "a.b"})
    p.send({"type": "a.b", "data": {"old": True}})
    p.send("not json")
    p.send({"type": "message", "payload": 1})
    p.send(msg("a.b", {"id": "a"}))
    until(lambda: got)
    time.sleep(0.05)
    assert got == [{"id": "a"}]


def test_star_one_segment_double_star_one_or_more(srv, streams):
    one, many = [], []
    s = open_stream(streams, srv.url, "a.**")
    s.on("a.*", one.append)
    s.on("a.**", many.append)
    until(lambda: srv.peers and srv.peers[0].received)
    for t in ["a.b", "a.b.c", "a", "x.b"]:
        srv.peers[0].send(msg(t, t))
    until(lambda: len(many) == 2)
    time.sleep(0.05)
    assert one == ["a.b"]
    assert many == ["a.b", "a.b.c"]


def test_bare_star_subscribes_double_star_and_delivers_all(srv, streams):
    s = open_stream(streams, srv.url)
    got = []
    s.on("*", got.append)
    until(lambda: srv.peers and srv.peers[0].received)
    assert srv.peers[0].received[0] == {"type": "subscribe", "topic": "**"}
    srv.peers[0].send(msg("a", 1))
    srv.peers[0].send(msg("a.b.c", 2))
    until(lambda: got == [1, 2])


def test_stream_topic_filters_client_side(srv, streams):
    s = open_stream(streams, srv.url, "a.*")
    got = []
    s.on("*", got.append)
    until(lambda: srv.peers and srv.peers[0].received)
    srv.peers[0].send(msg("a.b.c", 1))
    srv.peers[0].send(msg("a.b", 2))
    until(lambda: got)
    time.sleep(0.05)
    assert got == [2]


def test_sends_bearer_token(srv, streams):
    open_stream(streams, srv.url, "a.b", token="tok")
    until(lambda: srv.peers)
    assert srv.peers[0].headers["authorization"] == "Bearer tok"


def test_reconnects_and_resubscribes_after_drop(srv, streams):
    s = open_stream(streams, srv.url, "a.b")
    got = []
    s.on("*", got.append)
    until(lambda: srv.peers and srv.peers[0].received)
    dropped = time.time()
    srv.peers[0].drop()
    until(lambda: len(srv.peers) == 2 and srv.peers[1].received, 6)
    assert srv.peers[1].received[0] == {"type": "subscribe", "topic": "a.b"}
    assert time.time() - dropped >= 0.9  # 1s backoff, not a hot loop
    srv.peers[1].send(msg("a.b", 1))
    until(lambda: got == [1])


def test_first_connect_failure_raises():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    start = time.time()
    with pytest.raises(ConnectionError, match="events connect"):
        EventStream(f"ws://127.0.0.1:{port}/events", "a.b")
    assert time.time() - start < 5  # fails fast, no retry loop


def test_close_stops_reconnect(srv, streams):
    s = open_stream(streams, srv.url, "a.b")
    until(lambda: srv.peers)
    s.close()
    time.sleep(1.5)
    assert len(srv.peers) == 1
