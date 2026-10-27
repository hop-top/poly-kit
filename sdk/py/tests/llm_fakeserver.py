"""A loopback HTTP/1.1 server answering canned responses, for the llm
adapter tests. Nothing leaves the machine: every test points an adapter
at this server. Responses are served in order; the last one repeats.
"""

from __future__ import annotations

import json
import threading
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


@dataclass
class Canned:
    status: int = 200
    headers: dict[str, str] = field(default_factory=dict)
    body: str = ""

    @classmethod
    def json(cls, status: int, body: str | dict[str, Any]) -> Canned:
        text = body if isinstance(body, str) else json.dumps(body)
        return cls(status, {"content-type": "application/json"}, text)

    @classmethod
    def sse(cls, body: str) -> Canned:
        return cls(200, {"content-type": "text/event-stream"}, body)


@dataclass
class Captured:
    path: str
    headers: dict[str, str]
    body: bytes

    def json(self) -> Any:
        return json.loads(self.body)


class Server:
    def __init__(self, responses: list[Canned]) -> None:
        assert responses
        self.responses = responses
        self.requests: list[Captured] = []
        self._lock = threading.Lock()
        outer = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args: Any) -> None:
                pass

            def do_GET(self) -> None:
                self.do_POST()

            def do_POST(self) -> None:
                n = int(self.headers.get("content-length") or 0)
                body = self.rfile.read(n)
                with outer._lock:
                    outer.requests.append(
                        Captured(
                            self.path,
                            {k.lower(): v for k, v in self.headers.items()},
                            body,
                        )
                    )
                    i = min(len(outer.requests), len(outer.responses)) - 1
                    canned = outer.responses[i]
                data = canned.body.encode("utf-8")
                self.send_response(canned.status)
                for k, v in canned.headers.items():
                    self.send_header(k, v)
                self.send_header("content-length", str(len(data)))
                self.send_header("connection", "close")
                self.end_headers()
                self.wfile.write(data)
                self.close_connection = True

        self._srv = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._srv.daemon_threads = True
        self.url = f"http://127.0.0.1:{self._srv.server_address[1]}"
        self._thread = threading.Thread(
            target=self._srv.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True
        )
        self._thread.start()

    @property
    def attempts(self) -> int:
        with self._lock:
            return len(self.requests)

    def only(self) -> Captured:
        assert len(self.requests) == 1, f"want exactly one request, got {len(self.requests)}"
        return self.requests[0]

    def close(self) -> None:
        self._srv.shutdown()
        self._srv.server_close()

    def __enter__(self) -> Server:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


def serve(*responses: Canned) -> Server:
    return Server(list(responses))
