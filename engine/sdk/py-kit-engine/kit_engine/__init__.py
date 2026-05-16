"""kit-engine: Python SDK for kit serve sidecar."""

from __future__ import annotations

import json
import subprocess
import time
from typing import TYPE_CHECKING

import requests

if TYPE_CHECKING:
    from kit_engine.collection import Collection
    from kit_engine.events import EventStream
    from kit_engine.identity import IdentityClient
    from kit_engine.peers import PeerClient
    from kit_engine.sync import SyncClient


class KitEngine:
    """Manages a kit serve sidecar process."""

    def __init__(
        self,
        port: int,
        pid: int,
        process: subprocess.Popen | None = None,
        token: str | None = None,
        shutdown_token: str | None = None,
    ):
        self.port = port
        self.pid = pid
        self._process = process
        self._token = token
        self._shutdown_token = shutdown_token
        self._base_url = f"http://localhost:{port}"

    @classmethod
    def start(
        cls,
        *,
        app: str = "default",
        data: str | None = None,
        port: int = 0,
        daemon: bool = False,
        encrypt: bool = False,
        no_peer: bool = False,
        no_sync: bool = False,
        bin_path: str | None = None,
    ) -> KitEngine:
        """Spawn kit serve and return connected instance."""
        if bin_path:
            binary = bin_path
        else:
            from kit_engine._binary import find_kit_binary

            binary = find_kit_binary()

        cmd = [binary, "serve", "--port", str(port), "--app", app]
        if data:
            cmd.extend(["--data", data])
        for flag, on in [
            ("--daemon", daemon),
            ("--encrypt", encrypt),
            ("--no-peer", no_peer),
            ("--no-sync", no_sync),
        ]:
            if on:
                cmd.append(flag)

        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        line = proc.stdout.readline()  # type: ignore[union-attr]
        if not line:
            proc.kill()
            raise RuntimeError("kit serve produced no output")

        info = json.loads(line)
        engine = cls(
            port=info["port"],
            pid=info["pid"],
            process=proc,
            token=info.get("token"),
            shutdown_token=info.get("shutdown_token"),
        )

        for _ in range(20):
            try:
                resp = requests.get(f"{engine._base_url}/health", timeout=1)
                if resp.status_code == 200:
                    return engine
            except requests.ConnectionError:
                time.sleep(0.1)

        proc.kill()
        raise RuntimeError("kit serve failed health check")

    @classmethod
    def connect(
        cls,
        port: int,
        token: str | None = None,
        shutdown_token: str | None = None,
    ) -> KitEngine:
        """Connect to an already-running engine."""
        resp = requests.get(f"http://localhost:{port}/health", timeout=5)
        resp.raise_for_status()
        info = resp.json()
        return cls(
            port=port,
            pid=info.get("pid", 0),
            token=token,
            shutdown_token=shutdown_token,
        )

    def collection(self, type_name: str) -> Collection:
        from kit_engine.collection import Collection

        return Collection(self._base_url, type_name, self._token)

    @property
    def sync(self) -> SyncClient:
        from kit_engine.sync import SyncClient

        return SyncClient(self._base_url, self._token)

    @property
    def peers(self) -> PeerClient:
        from kit_engine.peers import PeerClient

        return PeerClient(self._base_url, self._token)

    @property
    def identity(self) -> IdentityClient:
        from kit_engine.identity import IdentityClient

        return IdentityClient(self._base_url, self._token)

    def events(self, topic: str = "*") -> EventStream:
        from kit_engine.events import EventStream

        ws_url = f"ws://localhost:{self.port}/events?topic={topic}"
        return EventStream(ws_url)

    def stop(self):
        """Gracefully shut down the engine."""
        import contextlib

        headers = {}
        tok = self._shutdown_token or self._token
        if tok:
            headers["Authorization"] = f"Bearer {tok}"
        with contextlib.suppress(requests.ConnectionError):
            requests.post(f"{self._base_url}/shutdown", headers=headers, timeout=5)
        if self._process:
            self._process.wait(timeout=5)
