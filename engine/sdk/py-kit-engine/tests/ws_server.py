"""Minimal loopback WebSocket server for event-stream tests."""

from __future__ import annotations

import base64
import contextlib
import hashlib
import json
import socket
import struct
import threading
import time

_GUID = b"258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


class Peer:
    def __init__(self, conn: socket.socket, headers: dict[str, str]):
        self.conn = conn
        self.headers = headers
        self.received: list[dict] = []

    def send(self, frame) -> None:
        data = (frame if isinstance(frame, str) else json.dumps(frame)).encode()
        head = (
            bytes([0x81, len(data)])
            if len(data) < 126
            else bytes([0x81, 126]) + struct.pack(">H", len(data))
        )
        self.conn.sendall(head + data)

    def drop(self) -> None:
        with contextlib.suppress(OSError):
            self.conn.shutdown(socket.SHUT_RDWR)
        self.conn.close()

    def _read(self) -> None:
        buf = b""
        while True:
            try:
                chunk = self.conn.recv(4096)
            except OSError:
                return
            if not chunk:
                return
            buf += chunk
            while len(buf) >= 2:
                n, off = buf[1] & 0x7F, 2
                if n == 126:
                    if len(buf) < 4:
                        break
                    n, off = struct.unpack(">H", buf[2:4])[0], 4
                if len(buf) < off + 4 + n:
                    break
                mask = buf[off : off + 4]
                body = bytes(b ^ mask[i % 4] for i, b in enumerate(buf[off + 4 : off + 4 + n]))
                if buf[0] & 0x0F == 8:
                    with contextlib.suppress(OSError):
                        self.conn.sendall(b"\x88\x00")
                    return
                if buf[0] & 0x0F == 1:
                    self.received.append(json.loads(body))
                buf = buf[off + 4 + n :]


class Server:
    def __init__(self) -> None:
        self.peers: list[Peer] = []
        self._sock = socket.socket()
        self._sock.bind(("127.0.0.1", 0))
        self._sock.listen(5)
        self.url = f"ws://127.0.0.1:{self._sock.getsockname()[1]}/events"
        threading.Thread(target=self._accept, daemon=True).start()

    def _accept(self) -> None:
        while True:
            try:
                conn, _ = self._sock.accept()
            except OSError:
                return
            req = b""
            while b"\r\n\r\n" not in req:
                req += conn.recv(4096)
            lines = req.decode().split("\r\n")[1:]
            headers = {k.lower(): v for k, v in (ln.split(": ", 1) for ln in lines if ": " in ln)}
            accept = base64.b64encode(
                hashlib.sha1(headers["sec-websocket-key"].encode() + _GUID).digest()
            ).decode()
            conn.sendall(
                (
                    "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"
                    f"Connection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n\r\n"
                ).encode()
            )
            peer = Peer(conn, headers)
            threading.Thread(target=peer._read, daemon=True).start()
            self.peers.append(peer)

    def close(self) -> None:
        self._sock.close()
        for p in self.peers:
            p.drop()


def until(cond, timeout: float = 5.0) -> None:
    end = time.time() + timeout
    while not cond():
        if time.time() > end:
            raise AssertionError("until: timeout")
        time.sleep(0.01)
