"""WebSocket event stream client."""

from __future__ import annotations

import json
import threading
from collections.abc import Callable


class EventStream:
    """Subscribe to real-time events from kit serve."""

    def __init__(self, ws_url: str):
        try:
            import websocket  # type: ignore[import-untyped]
        except ImportError as e:
            raise ImportError(
                "websocket-client required: pip install hop-top-kit-engine[ws]"
            ) from e

        self._handlers: dict[str, list[Callable]] = {}
        self._ws = websocket.WebSocketApp(
            ws_url,
            on_message=self._dispatch,
        )
        self._thread = threading.Thread(target=self._ws.run_forever, daemon=True)
        self._thread.start()

    def on(self, event: str, handler: Callable) -> None:
        self._handlers.setdefault(event, []).append(handler)

    def _dispatch(self, _ws, message: str) -> None:
        data = json.loads(message)
        event_type = data.get("type", "*")
        for h in self._handlers.get(event_type, []):
            h(data)
        for h in self._handlers.get("*", []):
            h(data)

    def close(self) -> None:
        self._ws.close()
